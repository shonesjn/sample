import { useState, useRef, useCallback, useEffect, useMemo } from "react";
import { TransportMethods, WebRTCTransport } from "./types";

export function useWebRTCTransport(): WebRTCTransport {
  const [connectionState, setConnectionState] = useState<
    "disconnected" | "connecting" | "connected" | "failed"
  >("disconnected");
  const connectionStateRef = useRef<
    "disconnected" | "connecting" | "connected" | "failed"
  >("disconnected");
  const transportMethod = useRef<TransportMethods>("webrtc");
  const dataChannelRef = useRef<RTCDataChannel | null>(null);
  const onDataCallbackRef = useRef<(data: any) => void>(null);
  const peerConnectionRef = useRef<RTCPeerConnection | null>(null);
  const signalingSocketRef = useRef<WebSocket | null>(null);
  const outputCanvasRef = useRef<HTMLCanvasElement | null>(null);
  const outputCanvasCtxRef = useRef<CanvasRenderingContext2D | null>(null);

  // Data receiver handler
  const onDataReceived = useCallback((callback: (data: any) => void) => {
    onDataCallbackRef.current = callback;
  }, []);

  const connect = useCallback(async (config: { serverUrl: string }) => {
    setConnectionState("connecting");
    connectionStateRef.current = "connecting";

    if (!outputCanvasRef.current) {
      outputCanvasRef.current = document.createElement("canvas");
      outputCanvasCtxRef.current = outputCanvasRef.current.getContext("2d");
    }

    // Setup Peer Connection
    const pc = new RTCPeerConnection({
      iceServers: [{ urls: "stun:stun.l.google.com:19302" }],
    });
    peerConnectionRef.current = pc;

    // Add Media Tracks to send video to the edge
    const outputStream = outputCanvasRef.current.captureStream(30);
    outputStream
      .getTracks()
      .forEach((track) => pc.addTrack(track, outputStream));

    // The server (pion/ClientIF) creates the DataChannel and the browser
    // receives it via ondatachannel. This is correct — pion calls
    // CreateDataChannel("data", nil) before creating the offer.
    pc.ondatachannel = (event) => {
      const dc = event.channel;
      dc.onmessage = (event) => {
        onDataCallbackRef.current?.(JSON.parse(event.data));
      };
      dc.onopen = () => console.log("Data channel open");
      dataChannelRef.current = dc;
    };

    // Setup Signaling via WebSockets.
    // config.serverUrl must be the full WebSocket URL including the
    // /initiate/webrtc/{userID} path that ClientIF registers.
    // Example: ws://192.168.55.1:9999/initiate/webrtc/user1
    const ws = new WebSocket(config.serverUrl);
    signalingSocketRef.current = ws;

    ws.onopen = async () => {
      console.log("Signaling WebSocket connected");
    };

    ws.onmessage = async (event) => {
      const message = JSON.parse(event.data);
      if (!message.webrtc_signal) return;

      const messageSignal = message.webrtc_signal;

      if (messageSignal.type === "candidate") {
        // The server sends ICE candidates as:
        //   { webrtc_signal: { type: "candidate", candidate: { candidate: "...", sdpMid: "...", ... } } }
        // messageSignal.candidate is already an RTCIceCandidateInit-shaped object.
        await pc.addIceCandidate(new RTCIceCandidate(messageSignal.candidate));

      } else if (messageSignal.type === "offer") {
        // messageSignal is { type: "offer", sdp: "v=0\r\n..." } which is a
        // valid RTCSessionDescriptionInit — pass it directly.
        await pc.setRemoteDescription(new RTCSessionDescription(messageSignal));
        const answer = await pc.createAnswer();
        await pc.setLocalDescription(answer);
        ws.send(
          JSON.stringify({
            webrtc_signal: { type: "answer", sdp: answer.sdp },
          }),
        );
      }
    };

    pc.onicecandidate = (event) => {
      if (event.candidate) {
        // IMPORTANT: event.candidate is an RTCIceCandidate class instance.
        // Passing the object directly to JSON.stringify may serialize as {}
        // in some browsers because the fields are not own enumerable properties.
        // Calling .toJSON() returns a plain RTCIceCandidateInit object
        // { candidate, sdpMid, sdpMLineIndex, usernameFragment } that the
        // Go backend's WebRTCSignal.ICE field (webrtc.ICECandidateInit) expects.
        ws.send(
          JSON.stringify({
            webrtc_signal: {
              type: "candidate",
              candidate: event.candidate.toJSON(),
            },
          }),
        );
      }
    };

    pc.onconnectionstatechange = () => {
      if (pc.connectionState === "connected") {
        setConnectionState("connected");
        connectionStateRef.current = "connected";
      } else if (pc.connectionState === "failed") {
        setConnectionState("failed");
        connectionStateRef.current = "failed";
      }
    };
  }, []);

  const sendFrame = useCallback((frameCanvas: HTMLCanvasElement) => {
    if (
      connectionStateRef.current === "connected" &&
      outputCanvasRef.current &&
      outputCanvasCtxRef.current
    ) {
      const outputCanvas = outputCanvasRef.current;
      const ctx = outputCanvasCtxRef.current;
      if (
        outputCanvas.width !== frameCanvas.width ||
        outputCanvas.height !== frameCanvas.height
      ) {
        outputCanvas.width = frameCanvas.width;
        outputCanvas.height = frameCanvas.height;
      }
      ctx.drawImage(frameCanvas, 0, 0);
      console.log("frame drawn to offload canvas");
    } else {
      console.log("sendFrame SKIPPED - condition failed");
    }
  }, []);

  const disconnect = useCallback(() => {
    peerConnectionRef.current?.close();
    signalingSocketRef.current?.close();
    setConnectionState("disconnected");
    connectionStateRef.current = "disconnected";
  }, []);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      disconnect();
    };
  }, [disconnect]);

  const offloadTransport = useMemo(
    () => ({
      connect,
      disconnect,
      sendFrame,
      onDataReceived,
      connectionState,
      transportMethod: transportMethod.current,
    }),
    [connect, disconnect, sendFrame, onDataReceived, connectionState],
  );
  return useMemo(
    () => ({
      pc: peerConnectionRef.current,
      offloadTransport,
    }),
    [offloadTransport],
  );
}
