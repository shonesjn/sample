import { Task } from "@framework/definitions";
import { useQuery } from "@tanstack/react-query";

interface FpsDropdownProps {
  taskID: string;
  selectedFps: number | null;
  onChange: React.ChangeEventHandler<HTMLSelectElement>;
}

export default function FpsDropdown({
  taskID,
  selectedFps,
  onChange,
}: FpsDropdownProps) {
  const {
    data: tasks,
    isLoading,
    isError,
  } = useQuery<Task[]>({
    queryKey: [taskID],
    queryFn: () => fetch(`/api/tasks`).then((res) => res.json()),
  });

  if (isLoading) {
    return <div>Loading FPS options...</div>;
  }

  if (isError) {
    return <div>Error loading FPS options</div>;
  }

  const fpsListFromTask = tasks!.find((task) => task.taskID === taskID)
    ?.constraints.fps;
  return (
    <select
      className="border border-gray-900 rounded-md p-2 bg-gray-100"
      name="fps"
      value={selectedFps || ""}
      onChange={onChange}
      disabled={!fpsListFromTask}
    >
      {fpsListFromTask!.map((fps: number) => (
        <option key={fps} value={fps}>
          {fps}
        </option>
      ))}
    </select>
  );
}
