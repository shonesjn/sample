import React from "react";

import { Task, VideoResolution } from "@framework/definitions";
import { useQuery } from "@tanstack/react-query";

interface ResolutionDropdownProps {
  taskID: string;
  selectedResolution: VideoResolution | null;
  onChange: React.ChangeEventHandler<HTMLSelectElement>;
}

const ResolutionDropdown: React.FC<ResolutionDropdownProps> = ({
  taskID,
  selectedResolution,
  onChange,
}) => {

  const {
    data: tasks,
    isLoading,
    isError,
  } = useQuery<Task[]>({
    queryKey: [taskID],
    queryFn: () => fetch(`/api/tasks`).then((res) => res.json()),
  });

  if (isLoading) {
    return <div>Loading resolution options...</div>;
  }
  if (isError) {
    return <div>Error loading resolution options</div>;
  }

  const resolutionList = tasks!.find((task) => task.taskID === taskID)
    ?.constraints.resolutions;

  return (
    <select
      className="border border-gray-900 rounded-md p-2 bg-gray-100"
      name="resolution"
      value={selectedResolution?.id || ""}
      onChange={onChange}
    >
      {resolutionList!.map((res: VideoResolution) => (
        <option
          key={res.id}
          value={res.id}
        >
          {res.label || `${res.width}x${res.height} (${res.aspectRatio})`}
        </option>
      ))}
    </select>
  );
};

export default ResolutionDropdown;
