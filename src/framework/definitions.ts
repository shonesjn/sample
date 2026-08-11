export interface Task {
  taskID: string;
  name: string;
  description: string;
  constraints: TaskConstraints;
}

export interface TaskConstraints {
  resolutions: VideoResolution[];
  fps: number[];
  networkMethods: string[];
}

export interface VideoResolution {
  id: string;
  width: number;
  height: number;
  aspectRatio: string;
  label?: string;
}
