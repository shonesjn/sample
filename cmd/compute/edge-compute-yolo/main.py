import cv2
import time
import grpc
import numpy as np
import traceback
import socket
import struct
import threading
import queue

import inference_pb2
import inference_pb2_grpc

import argparse

TCP_PORT = 5000
GRPC_SERVER_ADDR = '127.0.0.1:5005'
MODEL_PATH = "yolo26n.engine"


def read_frame_from_tcp(sock):
    """
    Read a JPEG frame from TCP socket using MSG_WAITALL to minimize system call overhead.
    Protocol: [frame_id_len:4][frame_id:N][jpeg_size:4][jpeg_data:N]
    Returns: (jpeg_data_bytes, frame_id)
    """
    try:
        id_len_data = sock.recv(4, socket.MSG_WAITALL)
        if len(id_len_data) < 4:
            return None, None

        frame_id_len = struct.unpack('>I', id_len_data)[0]
        if frame_id_len > 256:
            return None, None

        frame_id_data = sock.recv(frame_id_len, socket.MSG_WAITALL)
        if len(frame_id_data) < frame_id_len:
            return None, None

        frame_id = frame_id_data.decode('utf-8')

        size_data = sock.recv(4, socket.MSG_WAITALL)
        if len(size_data) < 4:
            return None, None

        frame_size = struct.unpack('>I', size_data)[0]
        if frame_size > 10_000_000:
            return None, None

        frame_data = sock.recv(frame_size, socket.MSG_WAITALL)
        if len(frame_data) < frame_size:
            return None, None

        return frame_data, frame_id
    except Exception:
        return None, None


def tcp_reader_worker(sock, q, stop_event):
    while not stop_event.is_set():
        try:
            frame, frame_id = read_frame_from_tcp(sock)
            if frame is None:
                print("TCP connection closed by peer (EOF). Terminating reader worker.")
                break
            
            try:
                q.put_nowait((frame, frame_id, int(time.time() * 1000)))
            except queue.Full:
                try:
                    q.get_nowait()
                except queue.Empty:
                    pass
                try:
                    q.put_nowait((frame, frame_id, int(time.time() * 1000)))
                except queue.Full:
                    pass
        except Exception as e:
            print(f"TCP reader worker thread error: {e}")
            break
    
    try:
        q.put(None, timeout=1.0)
    except Exception:
        pass


def generate_inference_stream(sock, model):
    """
    Generator that consumes the latest frame from the TCP reader worker,
    runs YOLO inference, and yields YoloInferenceResult messages.
    """
    frame_count = 0
    q = queue.Queue(maxsize=1)
    stop_event = threading.Event()

    reader_thread = threading.Thread(
        target=tcp_reader_worker, 
        args=(sock, q, stop_event), 
        daemon=True
    )
    reader_thread.start()

    try:
        while True:
            try:
                item = q.get(timeout=5.0)
            except queue.Empty:
                continue

            if item is None:
                print("Generator received shutdown sentinel. Exiting inference stream.")
                break

            jpeg_data, frame_id, python_receive_ms = item

            nparr = np.frombuffer(jpeg_data, np.uint8)
            frame = cv2.imdecode(nparr, cv2.IMREAD_COLOR)
            if frame is None:
                continue

            frame_count += 1
            if frame_count == 10:
                cv2.imwrite("/app/debug_frame.jpg", frame)
                print("Saved debug_frame.jpg!")

            yolo_start_ms = int(time.time() * 1000)
            if hasattr(model, 'predict'):
                results = list(model.predict(frame, stream=True, conf=0.25, verbose=False))
                yolo_end_ms = int(time.time() * 1000)
                inference_latency_ms = float(yolo_end_ms - yolo_start_ms)

                for r in results:
                    grpc_boxes = []
                    if len(r.boxes) > 0:
                        xyxy = r.boxes.xyxy.cpu().numpy()
                        confs = r.boxes.conf.cpu().numpy()
                        cls_ids = r.boxes.cls.cpu().numpy()

                        for i in range(len(xyxy)):
                            c_id = int(cls_ids[i].item())
                            if hasattr(model, 'names') and isinstance(model.names, dict):
                                c_name = str(model.names.get(c_id, f"Class_{c_id}"))
                            else:
                                c_name = str(c_id)

                            x1 = int(xyxy[i][0].item())
                            y1 = int(xyxy[i][1].item())
                            x2 = int(xyxy[i][2].item())
                            y2 = int(xyxy[i][3].item())
                            w = x2 - x1
                            h = y2 - y1

                            grpc_boxes.append(inference_pb2.YoloBoundingBox(
                                class_label=c_name,
                                confidence=float(confs[i].item()),
                                x=x1,
                                y=y1,
                                w=w,
                                h=h
                            ))
            else:
                # YOLOv5 AutoShape model
                detections = model([frame])
                yolo_end_ms = int(time.time() * 1000)
                inference_latency_ms = float(yolo_end_ms - yolo_start_ms)

                grpc_boxes = []
                pred = detections.xyxy[0]
                if pred is not None and len(pred) > 0:
                    pred = pred.cpu().numpy()
                    for i in range(len(pred)):
                        x1, y1, x2, y2, conf, c_id = pred[i]
                        c_id = int(c_id)
                        if hasattr(model, 'names') and isinstance(model.names, dict) and c_id in model.names:
                            c_name = str(model.names[c_id])
                        else:
                            c_name = str(c_id)

                        grpc_boxes.append(inference_pb2.YoloBoundingBox(
                            class_label=c_name,
                            confidence=float(conf),
                            x=int(x1),
                            y=int(y1),
                            w=int(x2 - x1),
                            h=int(y2 - y1)
                        ))

            yield inference_pb2.YoloInferenceResult(
                timestamp=python_receive_ms,
                boxes=grpc_boxes,
                frame_id=frame_id,
                inference_latency_ms=inference_latency_ms,
                yolo_start_ms=yolo_start_ms,
                yolo_end_ms=yolo_end_ms,
            )
    except Exception as e:
        print("\n" + "="*50)
        print("CRITICAL PYTHON ERROR INSIDE GENERATOR:")
        traceback.print_exc()
        print("="*50 + "\n")
        raise e
    finally:
        stop_event.set()


def run(model_path="yolo26n.engine", tcp_port=5000, tracker_addr="127.0.0.1:5005"):
    print(f"Loading PyTorch model: {model_path}...")
    try:
        from ultralytics import YOLO
        trt_model = YOLO(model_path, task='detect')
    except Exception:
        import sys, types
        class DummyModule(types.ModuleType):
            def __init__(self, name):
                super().__init__(name)
                self.__path__ = []
            def __getattr__(self, name):
                return DummyModule(name)
            def __call__(self, *args, **kwargs):
                return DummyModule('dummy')
            def __enter__(self):
                return self
            def __exit__(self, *args):
                pass
            def update(self, *args, **kwargs):
                pass
        for mod in ['pandas', 'requests', 'yaml', 'tqdm', 'tqdm.auto', 'matplotlib', 'matplotlib.pyplot', 'seaborn', 'IPython', 'IPython.display', 'psutil']:
            sys.modules[mod] = DummyModule(mod)
        import platform
        platform.python_version = lambda: '3.8.0'
        import torch
        print("Ultralytics package not present, using PyTorch torch.hub YOLOv5n on GPU...")
        trt_model = torch.hub.load('ultralytics/yolov5:v7.0', 'yolov5n', pretrained=True, skip_validation=True)
        try:
            import sys
            sys.path.insert(0, '/root/.cache/torch/hub/ultralytics_yolov5_v7.0')
            from models.common import AutoShape
            if not isinstance(trt_model, AutoShape):
                print("Manually wrapping model in AutoShape...")
                trt_model = AutoShape(trt_model)
        except Exception as e:
            print("Failed to manually wrap model in AutoShape:")
            traceback.print_exc()

    server_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server_sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server_sock.bind(('0.0.0.0', tcp_port))
    server_sock.listen(1)
    print(f"TCP server listening on port {tcp_port} for JPEG frames from compute")

    while True:
        print(f"Waiting for compute TCP connection...")
        conn, addr = server_sock.accept()
        print(f"Compute connected from {addr}")
        conn.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)

        print(f"Connecting to Go gRPC server at {tracker_addr}...")
        try:
            grpc_options = [('grpc.tcp_nodelay', 1)]
            with grpc.insecure_channel(tracker_addr, options=grpc_options) as channel:
                stub = inference_pb2_grpc.InferenceTrackerStub(channel)

                print("Streaming inferences to Go server...")
                response = stub.StreamResults(
                    generate_inference_stream(conn, trt_model))

                print("StreamResults returned")

        except grpc.RpcError as e:
            print(f"gRPC connection lost: {e}. Reconnecting in 2 seconds")
            time.sleep(2)
        except KeyboardInterrupt:
            print("Shutting down cleanly.")
            break
        except Exception as e:
            print(f"Unexpected error: {e}")
            traceback.print_exc()
            time.sleep(2)
        finally:
            conn.close()
            print("Compute connection closed")

    server_sock.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", type=str, default="yolo26n.engine")
    parser.add_argument("--tcp-port", type=int, default=5000)
    parser.add_argument("--tracker-port", type=int, default=5005)
    args = parser.parse_args()

    run(model_path=args.model, tcp_port=args.tcp_port, tracker_addr=f"127.0.0.1:{args.tracker_port}")