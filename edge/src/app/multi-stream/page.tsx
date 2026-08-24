"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import UploadPanel from "./ui/UploadPanel";
import GlobalControls from "./ui/GlobalControls";
import AggregateStats from "./ui/AggregateStats";
import StreamTile from "./ui/StreamTile";
import { MultiStreamConfig, StreamStats, VideoSource } from "./definitions";

// Matches the scheduler's WebSocket listener (cmd/scheduler -port flag,
// default :9998), which exposes /ws/{streamId} directly (bypassing clientif).
const DEFAULT_SCHEDULER_WS =
  process.env.NEXT_PUBLIC_SCHEDULER_WS_URL ?? "ws://localhost:9998";

const DEFAULT_CONFIG: MultiStreamConfig = {
  schedulerBaseUrl: DEFAULT_SCHEDULER_WS,
  streamCount: 20,
  targetFps: 4.3,
  jpegQuality: 0.5,
  maxWidth: 416,
  staggerMs: 100,
};

let sourceIdCounter = 0;

export default function MultiStreamPage() {
  const [sources, setSources] = useState<VideoSource[]>([]);
  const [config, setConfig] = useState<MultiStreamConfig>(DEFAULT_CONFIG);
  const [running, setRunning] = useState(false);
  const [activeStreamIds, setActiveStreamIds] = useState<Set<string>>(new Set());
  const [statsMap, setStatsMap] = useState<Record<string, StreamStats>>({});
  const staggerTimers = useRef<ReturnType<typeof setTimeout>[]>([]);
  const sourcesRef = useRef<VideoSource[]>([]);
  sourcesRef.current = sources;

  const handleAddVideos = useCallback((files: FileList) => {
    const newSources: VideoSource[] = Array.from(files).map((file) => ({
      id: `src-${sourceIdCounter++}`,
      name: file.name,
      url: URL.createObjectURL(file),
      file,
    }));
    setSources((prev) => [...prev, ...newSources]);
  }, []);

  const handleRemoveSource = useCallback((id: string) => {
    setSources((prev) => {
      const target = prev.find((s) => s.id === id);
      if (target) URL.revokeObjectURL(target.url);
      return prev.filter((s) => s.id !== id);
    });
  }, []);

  // Revoke any remaining object URLs when the page unmounts.
  useEffect(() => {
    return () => {
      sourcesRef.current.forEach((s) => URL.revokeObjectURL(s.url));
    };
  }, []);

  const tiles = useMemo(() => {
    if (sources.length === 0) return [];
    return Array.from({ length: config.streamCount }, (_, i) => {
      const source = sources[i % sources.length];
      return {
        streamId: `sim-${i + 1}`,
        videoFile: source.file,
        videoName: source.name,
      };
    });
  }, [sources, config.streamCount]);

  const handleStartAll = useCallback(() => {
    staggerTimers.current.forEach(clearTimeout);
    staggerTimers.current = [];
    setRunning(true);

    tiles.forEach((tile, index) => {
      const timer = setTimeout(() => {
        setActiveStreamIds((prev) => {
          const next = new Set(prev);
          next.add(tile.streamId);
          return next;
        });
      }, index * config.staggerMs);
      staggerTimers.current.push(timer);
    });
  }, [tiles, config.staggerMs]);

  const handleStopAll = useCallback(() => {
    staggerTimers.current.forEach(clearTimeout);
    staggerTimers.current = [];
    setRunning(false);
    setActiveStreamIds(new Set());
  }, []);

  const handleStatsUpdate = useCallback((streamId: string, streamStats: StreamStats) => {
    setStatsMap((prev) => ({ ...prev, [streamId]: streamStats }));
  }, []);

  const disabledReason =
    sources.length === 0 ? "Upload at least one video to start." : null;

  return (
    <div className="p-4 space-y-4 w-full">
      <div>
        <h1 className="text-2xl font-bold">Multi-Stream Video Lab</h1>
        <p className="text-sm text-gray-400 mt-1 max-w-3xl">
          Upload traffic / crowd footage and simulate up to 40 concurrent
          camera streams against the ASET scheduler. Each tile opens its own
          WebSocket to <code>{"{scheduler}"}/ws/{"{streamId}"}</code>, loops
          its assigned clip, and streams JPEG frames independently — the same
          protocol a browser webcam client uses — so you can watch how the
          scheduler&rsquo;s window scheduling, DQN node selection, and
          per-node queues behave under real multi-stream load.
        </p>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <UploadPanel
          sources={sources}
          onAdd={handleAddVideos}
          onRemove={handleRemoveSource}
          disabled={running}
        />
        <GlobalControls
          config={config}
          onConfigChange={setConfig}
          running={running}
          onStartAll={handleStartAll}
          onStopAll={handleStopAll}
          disabledReason={disabledReason}
        />
      </div>

      <AggregateStats stats={Object.values(statsMap)} />

      {tiles.length === 0 ? (
        <div className="text-sm text-gray-500 border border-dashed border-gray-700 rounded-md p-8 text-center">
          Upload one or more videos above, then click &ldquo;Start all
          streams&rdquo;.
        </div>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5 gap-3">
          {tiles.map((tile) => (
            <StreamTile
              key={tile.streamId}
              streamId={tile.streamId}
              videoFile={tile.videoFile}
              videoName={tile.videoName}
              config={config}
              active={activeStreamIds.has(tile.streamId)}
              onStats={handleStatsUpdate}
            />
          ))}
        </div>
      )}
    </div>
  );
}
