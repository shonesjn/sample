"use client";

import { StreamStats } from "../definitions";

interface AggregateStatsProps {
  stats: StreamStats[];
}

export default function AggregateStats({ stats }: AggregateStatsProps) {
  const connected = stats.filter((s) => s.connectionState === "connected").length;
  const failed = stats.filter((s) => s.connectionState === "failed").length;
  const totalSentPerSec = stats.reduce((sum, s) => sum + s.framesSentPerSec, 0);
  const totalResultsPerSec = stats.reduce((sum, s) => sum + s.resultsPerSec, 0);
  const totalFramesSent = stats.reduce((sum, s) => sum + s.framesSent, 0);
  const latencies = stats
    .map((s) => s.lastInferenceLatencyMs)
    .filter((v): v is number => typeof v === "number");
  const avgLatency =
    latencies.length > 0
      ? latencies.reduce((a, b) => a + b, 0) / latencies.length
      : null;

  const cards: Array<{ label: string; value: string }> = [
    { label: "Active / total streams", value: `${connected}/${stats.length}` },
    { label: "Failed", value: String(failed) },
    { label: "Frames sent /s", value: String(totalSentPerSec) },
    { label: "Results /s", value: String(totalResultsPerSec) },
    { label: "Total frames sent", value: String(totalFramesSent) },
    {
      label: "Avg inference latency",
      value: avgLatency != null ? `${avgLatency.toFixed(1)} ms` : "—",
    },
  ];

  return (
    <div className="grid grid-cols-3 md:grid-cols-6 gap-2">
      {cards.map((c) => (
        <div key={c.label} className="bg-gray-800 rounded-md p-2 text-center">
          <div className="text-lg font-mono text-white">{c.value}</div>
          <div className="text-[10px] text-gray-200">{c.label}</div>
        </div>
      ))}
    </div>
  );
}
