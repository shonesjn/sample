"use client";

import { useEffect } from "react";
import CanvasDisplay from "@/app/video-processing/ui/CanvasDisplay";
import { useSimulatedStream } from "../hooks/useSimulatedStream";
import { MultiStreamConfig, StreamStats } from "../definitions";

interface StreamTileProps {
  streamId: string;
  videoFile: File;
  videoName: string;
  config: MultiStreamConfig;
  active: boolean;
  onStats: (streamId: string, stats: StreamStats) => void;
}

const STATE_COLOR: Record<string, string> = {
  idle: "bg-gray-500",
  connecting: "bg-yellow-500 animate-pulse",
  reconnecting: "bg-yellow-500 animate-pulse",
  connected: "bg-green-500",
  failed: "bg-red-500",
  stopped: "bg-gray-500",
};

export default function StreamTile({
  streamId,
  videoFile,
  videoName,
  config,
  active,
  onStats,
}: StreamTileProps) {
  const { videoRef, sentDims, boxes, stats } = useSimulatedStream({
    streamId,
    videoFile,
    config,
    active,
    onStats,
  });

  // Ensure the aggregate dashboard drops this tile's contribution once it
  // stops being rendered (e.g. stream count reduced).
  useEffect(() => {
    return () => {
      onStats(streamId, { ...stats, connectionState: "stopped" });
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [streamId]);

  return (
    <div className="relative w-full aspect-video bg-black rounded-md overflow-hidden border border-gray-700">
      <video
        ref={videoRef}
        muted
        loop
        autoPlay
        playsInline
        className="absolute inset-0 w-full h-full object-contain"
      />
      <CanvasDisplay
        boundingBoxes={boxes}
        videoWidth={sentDims.width || null}
        videoHeight={sentDims.height || null}
      />

      <div className="absolute top-1 left-1 flex items-center gap-1.5 bg-black/60 rounded px-1.5 py-0.5 text-[10px] text-white">
        <span
          className={`inline-block w-2 h-2 rounded-full ${STATE_COLOR[stats.connectionState] ?? "bg-gray-500"}`}
        />
        <span className="font-mono">{streamId}</span>
      </div>

      <div className="absolute top-1 right-1 bg-black/60 rounded px-1 py-0.5 text-[9px] text-gray-300 truncate max-w-[45%]">
        {videoName}
      </div>

      <div className="absolute bottom-1 left-1 right-1 flex justify-between items-center bg-black/60 rounded px-1.5 py-0.5 text-[10px] text-white font-mono">
        <span title="Frames sent/sec">↑{stats.framesSentPerSec}fps</span>
        <span title="Results received/sec">↓{stats.resultsPerSec}/s</span>
        <span title="Detections in last result">{stats.lastDetectionCount} obj</span>
        {stats.lastInferenceLatencyMs != null && (
          <span title="Server-side YOLO inference latency">
            {stats.lastInferenceLatencyMs.toFixed(0)}ms
          </span>
        )}
      </div>

      {stats.connectionState === "failed" && (
        <div className="absolute inset-0 flex items-center justify-center bg-black/75 text-red-400 text-[11px] p-2 text-center">
          {stats.lastError ?? "Connection failed"}
        </div>
      )}
    </div>
  );
}
