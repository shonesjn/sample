"use client";

import { useRef } from "react";
import { VideoSource } from "../definitions";

interface UploadPanelProps {
  sources: VideoSource[];
  onAdd: (files: FileList) => void;
  onRemove: (id: string) => void;
  disabled?: boolean;
}

export default function UploadPanel({ sources, onAdd, onRemove, disabled }: UploadPanelProps) {
  const inputRef = useRef<HTMLInputElement>(null);

  return (
    <div className="p-3 border border-gray-700 rounded-md">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold">Source videos</h3>
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          disabled={disabled}
          className="px-3 py-1 text-xs bg-blue-600 hover:bg-blue-500 disabled:bg-gray-600 disabled:cursor-not-allowed text-white rounded"
        >
          Upload video(s)
        </button>
        <input
          ref={inputRef}
          type="file"
          accept="video/*"
          multiple
          className="hidden"
          onChange={(e) => {
            if (e.target.files && e.target.files.length > 0) {
              onAdd(e.target.files);
            }
            e.target.value = "";
          }}
        />
      </div>

      {sources.length === 0 ? (
        <p className="text-xs text-gray-400">
          Upload traffic / crowd clips (mp4, webm, mov). Each simulated stream
          loops one of these clips and sends it to the scheduler as its own
          independent video feed.
        </p>
      ) : (
        <ul className="space-y-1 max-h-40 overflow-y-auto">
          {sources.map((s) => (
            <li
              key={s.id}
              className="flex items-center justify-between text-xs bg-gray-800 rounded px-2 py-1"
            >
              <span className="truncate">{s.name}</span>
              <button
                type="button"
                onClick={() => onRemove(s.id)}
                disabled={disabled}
                className="text-red-400 hover:text-red-300 disabled:text-gray-600 ml-2"
              >
                ✕
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
