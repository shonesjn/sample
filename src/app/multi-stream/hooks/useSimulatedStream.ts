"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import {
  MultiStreamConfig,
  StreamConnectionState,
  StreamDetectionBox,
  StreamStats,
  defaultStreamStats,
} from "../definitions";

// A singleton background worker timer that ticks at a fixed interval (33ms for ~30 FPS)
// and allows multiple stream subscribers to listen to tick events.
class GlobalBackgroundTimer {
  private worker: Worker | null = null;
  private listeners: Set<() => void> = new Set();
  private blobUrl: string | null = null;
  private fallbackIntervalId: ReturnType<typeof setInterval> | null = null;

  constructor() {
    if (typeof window === "undefined") return;

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

    try {
      const blob = new Blob([workerCode], { type: "application/javascript" });
      this.blobUrl = URL.createObjectURL(blob);
      const WorkerClass = window.Worker;
      this.worker = new WorkerClass(this.blobUrl);
      this.worker.onmessage = (e) => {
        if (e.data === "tick") {
          this.tick();
        }
      };
      // Start global worker at 5ms high-resolution tick interval
      this.worker.postMessage({ action: "start", interval: 5 });
    } catch (err) {
      console.warn("Failed to create GlobalBackgroundTimer worker, falling back to main-thread setInterval", err);
      // Fallback: Start a main-thread setInterval at 5ms
      this.fallbackIntervalId = setInterval(() => {
        this.tick();
      }, 5);
    }
  }

  private tick() {
    this.listeners.forEach((listener) => {
      try {
        listener();
      } catch (err) {
        console.error("GlobalBackgroundTimer listener error:", err);
      }
    });
  }

  public subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  public terminate() {
    if (this.worker) {
      this.worker.postMessage({ action: "stop" });
      this.worker.terminate();
      this.worker = null;
    }
    if (this.fallbackIntervalId) {
      clearInterval(this.fallbackIntervalId);
      this.fallbackIntervalId = null;
    }
    if (this.blobUrl) {
      URL.revokeObjectURL(this.blobUrl);
      this.blobUrl = null;
    }
  }
}

const globalBackgroundTimer = typeof window !== "undefined" ? new GlobalBackgroundTimer() : null;


interface UseSimulatedStreamArgs {
  streamId: string;
  videoFile: File;
  config: MultiStreamConfig;
  active: boolean;
  onStats?: (streamId: string, stats: StreamStats) => void;
}

interface UseSimulatedStreamResult {
  videoRef: React.RefObject<HTMLVideoElement | null>;
  videoDims: { width: number; height: number };
  sentDims: { width: number; height: number };
  boxes: StreamDetectionBox[];
  stats: StreamStats;
}

const MAX_RECONNECT_ATTEMPTS = 6;
const BASE_RECONNECT_DELAY_MS = 800;
const MAX_RECONNECT_DELAY_MS = 15000;

export function useSimulatedStream({
  streamId,
  videoFile,
  config,
  active,
  onStats,
}: UseSimulatedStreamArgs): UseSimulatedStreamResult {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const captureCanvasRef = useRef<HTMLCanvasElement | null>(null);
  const offscreenCanvasRef = useRef<OffscreenCanvas | null>(null);
  const isCapturingRef = useRef(false);
  const wsRef = useRef<WebSocket | null>(null);

  const configRef = useRef(config);
  configRef.current = config;

  // FIX: Use ref for onStats to prevent effect re-runs
  const onStatsRef = useRef(onStats);
  onStatsRef.current = onStats;

  const closingRef = useRef(false);
  const reconnectAttemptsRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const captureTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const framesSentRef = useRef(0);
  const framesSentWindowRef = useRef(0);
  const resultsReceivedRef = useRef(0);
  const resultsWindowRef = useRef(0);

  const [videoDims, setVideoDims] = useState({ width: 0, height: 0 });
  const [boxes, setBoxes] = useState<StreamDetectionBox[]>([]);
  const [stats, setStats] = useState<StreamStats>(defaultStreamStats(streamId));

  // Use ref for connection state to avoid effect re-runs
  const connStateRef = useRef<StreamConnectionState>("idle");
  const lastErrorRef = useRef<string | null>(null);

  const setConnState = useCallback((s: StreamConnectionState, error?: string | null) => {
    connStateRef.current = s;
    if (error !== undefined) {
      lastErrorRef.current = error;
    }
    setStats((prev) => ({
      ...prev,
      connectionState: s,
      lastError: error !== undefined ? error : prev.lastError,
    }));
  }, []);

  // Stable ref for setConnState
  const setConnStateRef = useRef(setConnState);
  setConnStateRef.current = setConnState;

  // Main WebSocket + capture effect
  useEffect(() => {
    if (!active) {
      return;
    }

    closingRef.current = false;
    reconnectAttemptsRef.current = 0;

    const video = videoRef.current;
    let onLoadedMeta: (() => void) | null = null;
    let localBlobUrl: string | null = null;

    if (video) {
      video.muted = true;
      video.loop = true;
      video.playsInline = true;
      video.autoplay = true;
      
      localBlobUrl = URL.createObjectURL(videoFile);
      video.src = localBlobUrl;

      onLoadedMeta = () => {
        setVideoDims({ width: video.videoWidth, height: video.videoHeight });
        // Stagger initial playback position across streams to prevent simultaneous end-of-clip looping stalls
        const streamNum = parseInt(streamId.replace(/\D/g, "") || "0", 10);
        if (video.duration > 1) {
          try {
            video.currentTime = (streamNum * 1.5) % (video.duration - 0.5);
          } catch {
            // Ignore seek errors if video is not ready
          }
        }
        video.play().catch(() => {});
      };
      video.addEventListener("loadedmetadata", onLoadedMeta);
      video.addEventListener("canplay", () => video.play().catch(() => {}));
      video.play().catch(() => {});
    }

    function captureAndSend() {
      if (isCapturingRef.current) return;

      const video = videoRef.current;
      const ws = wsRef.current;
      if (!video) return;

      // Seamless video loop check (rely on native video.loop = true for zero-stall GPU playback)
      if (video.ended) {
        video.currentTime = 0.01;
        video.play().catch(() => {});
      }

      // Auto-retry play if paused or stalled
      if (video.paused && video.readyState >= video.HAVE_METADATA) {
        video.play().catch(() => {});
      }

      if (video.readyState < video.HAVE_CURRENT_DATA) return;
      if (!ws || ws.readyState !== WebSocket.OPEN) return;

      // Congestion control backpressure: allow up to 1MB send buffer for multi-stream high throughput
      if (ws.bufferedAmount > 1024 * 1024) {
        return;
      }

      const vw = video.videoWidth;
      const vh = video.videoHeight;
      if (!vw || !vh) return;

      const maxWidth = configRef.current.maxWidth;
      let targetW = vw;
      let targetH = vh;
      if (maxWidth > 0 && vw > maxWidth) {
        const scale = maxWidth / vw;
        targetW = Math.max(1, Math.round(vw * scale));
        targetH = Math.max(1, Math.round(vh * scale));
      }

      // Hardware-accelerated GPU frame extraction via createImageBitmap
      if (typeof createImageBitmap !== "undefined" && typeof OffscreenCanvas !== "undefined") {
        isCapturingRef.current = true;
        createImageBitmap(video, { resizeWidth: targetW, resizeHeight: targetH, resizeQuality: "pixelated" })
          .then((bitmap) => {
            if (!offscreenCanvasRef.current) {
              offscreenCanvasRef.current = new OffscreenCanvas(targetW, targetH);
            }
            const oCanvas = offscreenCanvasRef.current;
            if (oCanvas.width !== targetW || oCanvas.height !== targetH) {
              oCanvas.width = targetW;
              oCanvas.height = targetH;
            }
            const ctx = oCanvas.getContext("2d");
            if (ctx) {
              ctx.drawImage(bitmap, 0, 0);
              bitmap.close(); // Immediately release GPU bitmap texture
              oCanvas.convertToBlob({ type: "image/jpeg", quality: configRef.current.jpegQuality })
                .then((blob) => {
                  if (blob && wsRef.current?.readyState === WebSocket.OPEN) {
                    wsRef.current.send(blob);
                    framesSentRef.current += 1;
                    framesSentWindowRef.current += 1;
                  }
                })
                .catch(() => {})
                .finally(() => {
                  isCapturingRef.current = false;
                });
            } else {
              bitmap.close();
              isCapturingRef.current = false;
            }
          })
          .catch(() => {
            isCapturingRef.current = false;
          });
        return;
      }

      if (!captureCanvasRef.current) {
        captureCanvasRef.current = document.createElement("canvas");
      }
      const canvas = captureCanvasRef.current;

      if (canvas.width !== targetW || canvas.height !== targetH) {
        canvas.width = targetW;
        canvas.height = targetH;
      }

      const ctx = canvas.getContext("2d");
      if (!ctx) return;
      ctx.drawImage(video, 0, 0, targetW, targetH);

      canvas.toBlob(
        (blob) => {
          if (blob && wsRef.current?.readyState === WebSocket.OPEN) {
            wsRef.current.send(blob);
            framesSentRef.current += 1;
            framesSentWindowRef.current += 1;
          }
        },
        "image/jpeg",
        configRef.current.jpegQuality,
      );
    }

    let unsubscribe: (() => void) | null = null;

    function stopCaptureLoop() {
      if (unsubscribe) {
        unsubscribe();
        unsubscribe = null;
      }
      if (captureTimerRef.current) {
        clearTimeout(captureTimerRef.current);
        captureTimerRef.current = null;
      }
    }

    function startCaptureLoop() {
      stopCaptureLoop();
      const fps = Math.max(1, configRef.current.targetFps);
      const streamNum = parseInt(streamId.replace(/\D/g, "") || "0", 10);
      const intervalMs = 1000 / fps;
      const offsetMs = (streamNum * 12) % intervalMs;

      if (globalBackgroundTimer) {
        let lastSendTime = performance.now() - offsetMs;
        unsubscribe = globalBackgroundTimer.subscribe(() => {
          if (!closingRef.current) {
            const currentFps = Math.max(1, configRef.current.targetFps);
            const targetInterval = 1000 / currentFps;
            const now = performance.now();
            if (now - lastSendTime >= targetInterval) {
              lastSendTime = now;
              captureAndSend();
            }
          }
        });
      } else {
        const tick = () => {
          captureAndSend();
          const fpsVal = Math.max(1, configRef.current.targetFps);
          captureTimerRef.current = setTimeout(tick, 1000 / fpsVal);
        };
        setTimeout(tick, offsetMs);
      }
    }

    function scheduleReconnect() {
      if (closingRef.current) return;
      if (reconnectAttemptsRef.current >= MAX_RECONNECT_ATTEMPTS) {
        setConnStateRef.current("failed", "Max reconnect attempts reached");
        return;
      }
      const attempt = reconnectAttemptsRef.current;
      reconnectAttemptsRef.current += 1;
      const delay = Math.min(
        BASE_RECONNECT_DELAY_MS * 2 ** attempt,
        MAX_RECONNECT_DELAY_MS,
      );
      reconnectTimerRef.current = setTimeout(connect, delay);
    }

    function connect() {
      if (closingRef.current) return;
      setConnStateRef.current(reconnectAttemptsRef.current > 0 ? "reconnecting" : "connecting", null);

      const base = configRef.current.schedulerBaseUrl.trim().replace(/\/+$/, "");
      const url = `${base}/ws/${encodeURIComponent(streamId)}`;

      let ws: WebSocket;
      try {
        ws = new WebSocket(url);
      } catch (err) {
        setConnStateRef.current("failed", String(err));
        scheduleReconnect();
        return;
      }
      wsRef.current = ws;

      ws.onopen = () => {
        reconnectAttemptsRef.current = 0;
        setConnStateRef.current("connected", null);
        startCaptureLoop();
      };

      ws.onmessage = (event) => {
        if (typeof event.data !== "string") return;
        try {
          const data = JSON.parse(event.data);
          resultsReceivedRef.current += 1;
          resultsWindowRef.current += 1;

          type RawDetection = {
            x: number;
            y: number;
            dx: number;
            dy: number;
            label: string;
            confidence: number;
          };
          const detections: RawDetection[] = Array.isArray(data.detections)
            ? data.detections
            : [];
          const mapped: StreamDetectionBox[] = detections.map((d) => ({
            x1: d.x,
            y1: d.y,
            x2: d.x + d.dx,
            y2: d.y + d.dy,
            label: d.label,
            confidence: d.confidence,
          }));
          setBoxes(mapped);

          setStats((prev) => ({
            ...prev,
            lastDetectionCount: mapped.length,
            lastInferenceLatencyMs:
              typeof data.inferenceLatencyMs === "number"
                ? data.inferenceLatencyMs
                : prev.lastInferenceLatencyMs,
            nodeName: typeof data.nodeName === "string" ? data.nodeName : prev.nodeName,
          }));
        } catch {
          // Ignore malformed / non-JSON payloads.
        }
      };

      ws.onerror = () => {
        setConnStateRef.current(connStateRef.current, "WebSocket error");
      };

      ws.onclose = () => {
        stopCaptureLoop();
        wsRef.current = null;
        if (closingRef.current) {
          setConnStateRef.current("stopped", null);
          return;
        }
        scheduleReconnect();
      };
    }

    connect();

    return () => {
      closingRef.current = true;
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      stopCaptureLoop();
      wsRef.current?.close();
      wsRef.current = null;

      if (video) {
        if (onLoadedMeta) video.removeEventListener("loadedmetadata", onLoadedMeta);
        video.pause();
        video.removeAttribute("src");
        video.load();
      }
      if (localBlobUrl) {
        URL.revokeObjectURL(localBlobUrl);
      }
      setConnStateRef.current("idle", null);
      setBoxes([]);
    };
  }, [active, videoFile, streamId]); // Only re-run when these change

  // Roll frames-sent / results-received counters into per-second rates.
  useEffect(() => {
    const interval = setInterval(() => {
      setStats((prev) => ({
        ...prev,
        framesSent: framesSentRef.current,
        framesSentPerSec: framesSentWindowRef.current,
        resultsReceived: resultsReceivedRef.current,
        resultsPerSec: resultsWindowRef.current,
      }));
      framesSentWindowRef.current = 0;
      resultsWindowRef.current = 0;
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  // Keep a ref to the latest stats so the interval can read it without re-running
  const statsRef = useRef(stats);
  statsRef.current = stats;

  // Stable stats update reporter to the parent component
  useEffect(() => {
    const interval = setInterval(() => {
      onStatsRef.current?.(streamId, statsRef.current);
    }, 500); // Stable 2Hz rate, never cleared/reset by stats changes
    return () => clearInterval(interval);
  }, [streamId]);

  // Calculate actual sent frame dimensions for coordinates scaling
  const maxWidth = config.maxWidth;
  let sentWidth = videoDims.width;
  let sentHeight = videoDims.height;
  if (maxWidth > 0 && videoDims.width > maxWidth) {
    const scale = maxWidth / videoDims.width;
    sentWidth = Math.max(1, Math.round(videoDims.width * scale));
    sentHeight = Math.max(1, Math.round(videoDims.height * scale));
  }

  return {
    videoRef,
    videoDims,
    sentDims: { width: sentWidth, height: sentHeight },
    boxes,
    stats,
  };
}