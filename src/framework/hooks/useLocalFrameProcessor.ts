import { useRef, useEffect, useCallback } from "react";

type LocalProcessingAlgorithm = (frame: ImageData) => ImageData;

export function useLocalFrameProcessor(
  localCameraStream: MediaStream | null,
  algorithm: LocalProcessingAlgorithm,
) {
  const localProcessedStream = useRef<MediaStream | null>(null);
  const workerRef = useRef<Worker>(null);

  useEffect(() => {
    workerRef.current = new Worker(
      new URL("@framework/workers/frame-processor.worker.ts", import.meta.url),
    );
    return () => workerRef.current?.terminate();
  }, []);

  useEffect(() => {
    if (!workerRef.current) return;
    if (!localCameraStream) return;

    const processFrame = async (frame: ImageData): Promise<ImageData> => {
      return new Promise((resolve) => {
        if (!workerRef.current) return;

        workerRef.current.postMessage(frame);

        workerRef.current.onmessage = (e) => {
          resolve(e.data);
        };
      });
    }

  }, [localCameraStream, algorithm]);


  return { localProcessedStream };
}
