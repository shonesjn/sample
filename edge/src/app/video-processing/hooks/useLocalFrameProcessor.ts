import { useRef, useEffect, useCallback } from "react";
import { VideoFrameData } from "@/app/video-processing/types/video-types";

// TODO: Try generalizing hook to be part of the library
export function useLocalFrameProcessor(kernel: number[]) {
  const workerRef = useRef<Worker>(null);

  useEffect(() => {
    workerRef.current = new Worker(
      new URL("@framework/workers/frame-processor.worker.ts", import.meta.url),
    );
    return () => workerRef.current?.terminate();
  }, []);

  const processFrame = useCallback(
    async (frame: ImageData): Promise<ImageData> => {
      return new Promise((resolve) => {
        if (!workerRef.current) return;

        workerRef.current.postMessage(
          {
            frame,
            kernel: kernel,
          },
          [frame.data.buffer],
        );

        workerRef.current.onmessage = (e) => {
          resolve(e.data);
        };
      });
    },
    [kernel],
  );

  return { processFrame };
}
