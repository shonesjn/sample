import { BenchmarkConfig } from "@/app/video-processing/definitions";
import FpsDropdown from "@framework/ui/FpsDropdown";
import NetworkMethodDropdown from "@framework/ui/NetworkMethodDropdown";
import ResolutionDropdown from "@framework/ui/ResolutionDropdown";
import { Task } from "@framework/definitions";
import { useQuery } from "@tanstack/react-query";

const taskID = "video-processing";

interface ConfigOptionsProps {
  currentConfig: BenchmarkConfig;
  onConfigChange: (newConfig: BenchmarkConfig) => void;
}

export default function ConfigOptions({
  currentConfig,
  onConfigChange,
}: ConfigOptionsProps) {
  const {
    data: tasksOptions,
    isLoading,
    isError,
  } = useQuery<Task[]>({
    queryKey: [taskID],
    queryFn: () => fetch(`/api/tasks`).then((res) => res.json()),
  });

  const handleChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const { name, value } = e.target;

    // Create to be updated value beased on value
    let valueUpdated: any = value;
    if (name === "resolution") {
      valueUpdated =
        tasksOptions!
          .find((task) => task.taskID === taskID)
          ?.constraints.resolutions.find((res) => res.id === value) || null;
    }

    const newConfig = {
      ...currentConfig,
      [name]: valueUpdated,
    };

    onConfigChange(newConfig);
  };

  return (
    <div className="flex mx-auto p-4">
      <div>
        <label htmlFor="resolution">Resolution:</label>
        <ResolutionDropdown
          taskID={taskID}
          selectedResolution={currentConfig.resolution}
          onChange={handleChange}
        />
      </div>

      <div>
        <label htmlFor="fps">FPS:</label>
        <FpsDropdown
          taskID={taskID}
          selectedFps={currentConfig.fps}
          onChange={handleChange}
        />
      </div>

      <div>
        <label htmlFor="networkMethod">Network Method:</label>
        <NetworkMethodDropdown
          taskID={taskID}
          selectedNetworkMethod={currentConfig.networkMethod}
          onChange={handleChange}
        />
      </div>
    </div>
  );
}
