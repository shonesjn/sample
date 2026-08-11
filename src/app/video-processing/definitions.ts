import { VideoResolution } from "@/framework/definitions";
import { TransportMethods } from "@/framework/transports/types";

export interface BenchmarkConfig {
  resolution: VideoResolution | null;
  fps: number | null;
  networkMethod: TransportMethods;
  offloadScheduler: string | null;
  taskScheduler: string | null;
  taskSchedulerBufferSize: number | null;
}
