"use client";
import { useEffect, useRef } from "react";
import { OffloadTransport } from "../transports/types";

type OffloadDecisionAlgorithm = (
  frame: ImageData,
  frameCount: number,
) => boolean;

/**
 * A hook that sits between a raw MediaStream and an OffloadTransport,
 * applying a decision algorithm to each frame and delegating the send action.
 * Uses a background Web Worker timer to bypass browser tab sleep/throttling when in the background.
 */
export function useOffloadScheduler(
  offloadStream: MediaStream | null,
  transport: OffloadTransport,
  algorithm: OffloadDecisionAlgorithm,
) {
  const frameCountRef = useRef(0);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);

  useEffect(() => {
    if (!offloadStream) return;

    if (!canvasRef.current) {
      canvasRef.current = document.createElement("canvas");
    }

    const canvas = canvasRef.current;
    const ctx = canvas.getContext("2d", { willReadFrequently: true });
    if (!ctx) {
      console.error("failed to get context");
      return;
    }

    if (!videoRef.current) {
      videoRef.current = document.createElement("video");
      videoRef.current.muted = true;
      videoRef.current.playsInline = true;
      videoRef.current.autoplay = true;
    }

    const video = videoRef.current;
    video.srcObject = offloadStream;

    const processFrame = () => {
      if (video.readyState >= video.HAVE_ENOUGH_DATA) {
        if (
          canvas.width !== video.videoWidth ||
          canvas.height !== video.videoHeight
        ) {
          canvas.width = video.videoWidth;
          canvas.height = video.videoHeight;
        }

        ctx.drawImage(video, 0, 0, canvas.width, canvas.height);

        const imageData = ctx.getImageData(0, 0, canvas.width, canvas.height);
        frameCountRef.current++;
        // Run the decision algorithm
        if (algorithm(imageData, frameCountRef.current)) {
          // Delegate: Call sendFrame on the transport.
          transport.sendFrame(canvas);
          console.log("offloaded frame", frameCountRef.current);
        }
      }
    };

    // Create inline worker code to run a background timer unaffected by tab suspension/throttling
    const workerCode = `
      let intervalId = null;
      self.onmessage = (e) => {
        if (e.data.action === 'start') {
          if (intervalId) clearInterval(intervalId);
          intervalId = setInterval(() => {
            self.postMessage('tick');
          }, e.data.interval);
        } else if (e.data.action === 'stop') {
          if (intervalId) {
            clearInterval(intervalId);
            intervalId = null;
          }
        }
      };
    `;

    let worker: Worker | null = null;
    let blobUrl: string | null = null;

    try {
      const blob = new Blob([workerCode], { type: "application/javascript" });
      blobUrl = URL.createObjectURL(blob);
      const WorkerClass = typeof window !== "undefined" ? window.Worker : Worker;
      worker = new WorkerClass(blobUrl);
      worker.onmessage = (e) => {
        if (e.data === "tick") {
          processFrame();
        }
      };
      // Start the worker timer at 33ms interval (~30 FPS)
      worker.postMessage({ action: "start", interval: 33 });
      console.log("Background Web Worker timer started at 33ms interval.");
    } catch (err) {
      console.error("Failed to start background Web Worker timer, falling back to main-thread setInterval:", err);
      const intervalId = setInterval(processFrame, 33);
      return () => {
        clearInterval(intervalId);
        video.pause();
        video.srcObject = null;
      };
    }

    const handleLoadedMetadata = () => {
      video.play().catch((error) => {
        console.error("Video play failed in Offload Scheduler:", error);
      });
    };

    // Wait for metadata to load before playing
    if (video.readyState >= video.HAVE_METADATA) {
      handleLoadedMetadata();
    } else {
      video.addEventListener("loadedmetadata", handleLoadedMetadata);
    }

    return () => {
      if (worker) {
        worker.postMessage({ action: "stop" });
        worker.terminate();
      }
      if (blobUrl) {
        URL.revokeObjectURL(blobUrl);
      }
      video.pause();
      video.srcObject = null;
    };
  }, [offloadStream, transport, algorithm]);
}
