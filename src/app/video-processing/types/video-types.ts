import { VideoResolution } from "@/framework/definitions";

// TODO: Generalize types and move to lib

export type BoundingBox = {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  confidence: number;
  label: string;
};

export type VideoFrameData = {
  frame: VideoFrame;
  timestamp: number;
  resolution: VideoResolution;
};
