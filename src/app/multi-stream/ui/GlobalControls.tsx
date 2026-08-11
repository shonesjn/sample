"use client";

import { MultiStreamConfig } from "../definitions";

interface GlobalControlsProps {
  config: MultiStreamConfig;
  onConfigChange: (c: MultiStreamConfig) => void;
  running: boolean;
  onStartAll: () => void;
  onStopAll: () => void;
  disabledReason: string | null;
}

export default function GlobalControls({
  config,
  onConfigChange,
  running,
  onStartAll,
  onStopAll,
  disabledReason,
}: GlobalControlsProps) {
  const update = (patch: Partial<MultiStreamConfig>) =>
    onConfigChange({ ...config, ...patch });

  return (
    <div className="p-3 border border-gray-700 rounded-md space-y-3">
      <h3 className="text-sm font-semibold">Simulation settings</h3>

      <div className="grid grid-cols-2 gap-3 text-xs">
        <label className="flex flex-col gap-1 col-span-2">
          Scheduler WebSocket URL
          <input
            type="text"
            value={config.schedulerBaseUrl}
            onChange={(e) => update({ schedulerBaseUrl: e.target.value })}
            disabled={running}
            className="bg-gray-800 rounded px-2 py-1 text-white disabled:opacity-50"
            placeholder="ws://<jetson-ip>:9998"
          />
        </label>

        <label className="flex flex-col gap-1">
          Concurrent streams: {config.streamCount}
          <input
            type="range"
            min={1}
            max={40}
            value={config.streamCount}
            onChange={(e) => update({ streamCount: Number(e.target.value) })}
            disabled={running}
          />
        </label>

        <label className="flex flex-col gap-1">
          Target FPS per stream: {config.targetFps}
          <input
            type="range"
            min={1}
            max={30}
            value={config.targetFps}
            onChange={(e) => update({ targetFps: Number(e.target.value) })}
          />
        </label>

        <label className="flex flex-col gap-1">
          JPEG quality: {config.jpegQuality.toFixed(2)}
          <input
            type="range"
            min={0.3}
            max={0.95}
            step={0.05}
            value={config.jpegQuality}
            onChange={(e) => update({ jpegQuality: Number(e.target.value) })}
          />
        </label>

        <label className="flex flex-col gap-1">
          Max capture width, px (0 = original)
          <input
            type="number"
            min={0}
            step={80}
            value={config.maxWidth}
            onChange={(e) => update({ maxWidth: Number(e.target.value) })}
          />
        </label>

        <label className="flex flex-col gap-1">
          Connection stagger (ms between stream starts)
          <input
            type="number"
            min={0}
            step={25}
            value={config.staggerMs}
            onChange={(e) => update({ staggerMs: Number(e.target.value) })}
            disabled={running}
          />
        </label>
      </div>

      <div className="flex items-center gap-2 pt-1">
        <button
          type="button"
          onClick={onStartAll}
          disabled={running || !!disabledReason}
          className="px-4 py-1.5 text-sm bg-green-600 hover:bg-green-500 disabled:bg-gray-600 disabled:cursor-not-allowed text-white rounded"
        >
          Start all streams
        </button>
        <button
          type="button"
          onClick={onStopAll}
          disabled={!running}
          className="px-4 py-1.5 text-sm bg-red-600 hover:bg-red-500 disabled:bg-gray-600 disabled:cursor-not-allowed text-white rounded"
        >
          Stop all
        </button>
        {disabledReason && !running && (
          <span className="text-xs text-yellow-400">{disabledReason}</span>
        )}
      </div>
    </div>
  );
}
