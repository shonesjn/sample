export type StreamConnectionState =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "failed"
  | "stopped";

export interface VideoSource {
  id: string;
  name: string;
  url: string;
  file: File;
}

export interface StreamDetectionBox {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  label: string;
  confidence: number;
}

export interface StreamStats {
  streamId: string;
  connectionState: StreamConnectionState;
  framesSent: number;
  framesSentPerSec: number;
  resultsReceived: number;
  resultsPerSec: number;
  lastInferenceLatencyMs: number | null;
  lastDetectionCount: number;
  lastError: string | null;
  nodeName: string | null;
}

export interface MultiStreamConfig {
  // Base WebSocket URL of the scheduler, e.g. ws://<jetson-ip>:9998
  // The hook appends /ws/{streamId} to this, matching cmd/scheduler wsHandler.
  schedulerBaseUrl: string;
  streamCount: number;
  targetFps: number;
  jpegQuality: number;
  // 0 disables downscaling and sends frames at native video resolution.
  maxWidth: number;
  // Delay in ms between starting each successive stream, to avoid a
  // connection storm against the scheduler when starting many streams at once.
  staggerMs: number;
}

export function defaultStreamStats(streamId: string): StreamStats {
  return {
    streamId,
    connectionState: "idle",
    framesSent: 0,
    framesSentPerSec: 0,
    resultsReceived: 0,
    resultsPerSec: 0,
    lastInferenceLatencyMs: null,
    lastDetectionCount: 0,
    lastError: null,
    nodeName: null,
  };
}
