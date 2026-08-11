import type { Task } from "@framework/definitions";
import { NextResponse } from "next/server";

// TODO: Move to a db
const tasks: Task[] = [
  {
    taskID: "video-processing",
    name: "Video Processing",
    description: "...",
    constraints: {
      resolutions: [
        { id: "480p", width: 640, height: 480, aspectRatio: "4:3", label: "480p (SD)" },
        { id: "720p", width: 1280, height: 720, aspectRatio: "16:9", label: "720p (HD)" },
        {
          id: "1080p",
          width: 1920,
          height: 1080,
          aspectRatio: "16:9",
          label: "1080p (Full HD)",
        },
        {
          id: "1440p",
          width: 2560,
          height: 1440,
          aspectRatio: "16:9",
          label: "1440p (QHD)",
        },
        { id: "4k", width: 3840, height: 2160, aspectRatio: "16:9", label: "2160p (4K)" },
      ],
      fps: [10, 30, 60],
      networkMethods: ["http", "websocket", "webrtc"],
    },
  },
  {
    taskID: "multi-stream",
    name: "Multi-Stream Video Lab",
    description:
      "Upload traffic/crowd video clips and simulate many concurrent camera streams against the scheduler.",
    constraints: {
      resolutions: [
        { id: "480p", width: 640, height: 480, aspectRatio: "4:3", label: "480p (SD)" },
        { id: "720p", width: 1280, height: 720, aspectRatio: "16:9", label: "720p (HD)" },
      ],
      fps: [1, 5, 10, 15, 20],
      networkMethods: ["websocket"],
    },
  },
  {
    taskID: "real-time-chat",
    name: "Real Time Chat",
    description: "...",
    constraints: {
      resolutions: [
        { id: "480p", width: 640, height: 480, aspectRatio: "4:3", label: "480p (SD)" },
        { id: "720p", width: 1280, height: 720, aspectRatio: "16:9", label: "720p (HD)" },
        {
          id: "1080p",
          width: 1920,
          height: 1080,
          aspectRatio: "16:9",
          label: "1080p (Full HD)",
        },
        {
          id: "1440p",
          width: 2560,
          height: 1440,
          aspectRatio: "16:9",
          label: "1440p (QHD)",
        },
        { id: "4k", width: 3840, height: 2160, aspectRatio: "16:9", label: "2160p (4K)" },

      ],
      fps: [10, 30, 60],
      networkMethods: ["http", "websocket", "webrtc"],
    },
  },
];

export async function GET() {
  return NextResponse.json(tasks);
}
