"use client";

import { useState, useEffect, useRef } from "react";
import {
  useCamera,
  useOffloadScheduler,
  useLocalFrameProcessor,
} from "@framework/hooks";
import useTransport from "@framework/transports/useTransport";
import CanvasDisplay from "@/app/video-processing/ui/CanvasDisplay";
import { BoundingBox } from "../video-processing/types/video-types";
import ConfigOptions from "./ui/ConfigOptions";
import { BenchmarkConfig } from "./definitions";
import { useTrackActualResolution } from "./hooks/useTrackActualResolution";
import { useMediaStreamToCanvasRef } from "./hooks/useMediaStreamToCanvas";

// Send every frame for smooth real-time offloading (30 FPS)
const everyFrame = (frame: ImageData, frameCount: number) => true;

// The ClientIF WebSocket signaling base URL.
const CLIENTIF_WS_BASE =
  process.env.NEXT_PUBLIC_CLIENTIF_WS_URL ?? 
  (typeof window !== "undefined" ? `ws://${window.location.hostname}:9999` : "ws://localhost:9999");

// Scheduler WebSocket URL for direct connection
// FIXED: Must match scheduler's WebSocket port (:9998)
const SCHEDULER_WS_BASE =
  process.env.NEXT_PUBLIC_SCHEDULER_WS_URL ?? 
  (typeof window !== "undefined" ? `ws://${window.location.hostname}:9998` : "ws://10.186.95.115:9998");

export default function BenchmarkPage() {
  const [benchmarkConfig, setBenchmarkConfig] = useState<BenchmarkConfig>({
    resolution: null,
    fps: null,
    networkMethod: "none",
    offloadScheduler: null,
    taskScheduler: null,
    taskSchedulerBufferSize: null,
  });
  const handleBenchmarkConfigChange = (newConfig: BenchmarkConfig) => {
    setBenchmarkConfig(newConfig);
    console.debug("Benchmark config updated:", newConfig);
  };

  const [boundingBoxes, setBoundingBoxes] = useState<BoundingBox[]>([]);

  const {
    cameraStream: rawCameraStream,
    startCamera,
    stopCamera,
  } = useCamera();

  const localCameraStreamRef = useRef<MediaStream | null>(null);
  const offloadCameraStreamRef = useRef<MediaStream | null>(null);
  useEffect(() => {
    if (!rawCameraStream) {
      localCameraStreamRef.current = null;
      offloadCameraStreamRef.current = null;
      return;
    }
    localCameraStreamRef.current = rawCameraStream.clone();
    offloadCameraStreamRef.current = rawCameraStream.clone();
  }, [rawCameraStream]);

  const { actualResolution } = useTrackActualResolution(rawCameraStream);

  // FIXED: Type assertion to satisfy useTransport overloads
  const { pc, offloadTransport } = useTransport(benchmarkConfig.networkMethod);

  useEffect(() => {
    offloadTransport.onDataReceived((data) => {
      if (!data?.detections) return;
      const boxes: BoundingBox[] = data.detections.map((d: any) => ({
        x1: d.x,
        y1: d.y,
        x2: d.x + d.dx,
        y2: d.y + d.dy,
        label: d.label,
        confidence: d.confidence,
      }));
      setBoundingBoxes(boxes);
    });
  }, [offloadTransport]);

  useOffloadScheduler(
    offloadCameraStreamRef.current,
    offloadTransport,
    everyFrame,
  );

  const { localProcessedStream } = useLocalFrameProcessor(
    localCameraStreamRef.current,
    (data) => data,
  );

  const handleStart = async () => {
    if (
      !benchmarkConfig.resolution ||
      !benchmarkConfig.fps ||
      !benchmarkConfig.networkMethod ||
      benchmarkConfig.networkMethod === "none"
    ) {
      alert("Please select resolution, fps, and network method.");
      return;
    }
    await startCamera(
      benchmarkConfig.resolution?.width || 640,
      benchmarkConfig.resolution?.height || 480,
      benchmarkConfig.fps || 30,
    );
    await handleConnect();
  };

  const handleConnect = async () => {
    if (benchmarkConfig.networkMethod === "none") {
      alert("Please select a network method.");
      return;
    }

    // FIXED: Use correct URL based on transport method
    let url: string;
    if (benchmarkConfig.networkMethod === "webrtc") {
      url = `${CLIENTIF_WS_BASE}/initiate/webrtc/user1`;
    } else if (benchmarkConfig.networkMethod === "websocket") {
      url = `${SCHEDULER_WS_BASE}/ws/user1`;
    } else {
      console.error("Unknown network method:", benchmarkConfig.networkMethod);
      return;
    }

    console.log("Connecting to:", url, "via", benchmarkConfig.networkMethod);
    await offloadTransport.connect({ serverUrl: url });
  };

  const rawCanvasRef = useMediaStreamToCanvasRef(rawCameraStream);

  return (
    <div>
      <h1>Video Processing Benchmarks</h1>
      <div className="container mx-auto p-4">
        <h2>Configuration options</h2>
        <ConfigOptions
          currentConfig={benchmarkConfig}
          onConfigChange={handleBenchmarkConfigChange}
        />
        <button
          onClick={handleStart}
          className="mt-4 px-4 py-2 bg-blue-500 text-white rounded-md"
        >
          Start Benchmark
        </button>
      </div>
      <div>
        <h2>Debug info</h2>
        <div className="mt-4 text-center">
          Connection Status: {offloadTransport?.connectionState}
        </div>
      </div>
      <div>
        <h2>Video Displays</h2>
        <div className="relative w-full max-w-4xl aspect-video bg-gray-900 rounded-lg overflow-hidden">
          <canvas
            ref={rawCanvasRef}
            className="absolute top-0 left-0 w-full h-full"
          />
          <CanvasDisplay
            boundingBoxes={boundingBoxes}
            videoWidth={actualResolution?.width || null}
            videoHeight={actualResolution?.height || null}
          />
        </div>
      </div>
    </div>
  );
}