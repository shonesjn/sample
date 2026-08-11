import { Task } from "@framework/definitions";
import { useQuery } from "@tanstack/react-query";

interface NetworkMethodDropdownProps {
  taskID: string;
  selectedNetworkMethod: string | null;
  onChange: React.ChangeEventHandler<HTMLSelectElement>;
}

export default function NetworkMethodDropdown({
  taskID,
  selectedNetworkMethod,
  onChange,
}: NetworkMethodDropdownProps) {
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

  const NetowrkMethodsFromTask = tasks!.find((task) => task.taskID === taskID)
    ?.constraints.networkMethods;

  return (
    <select
      name="networkMethod"
      value={selectedNetworkMethod || ""}
      onChange={onChange}
      className="border border-gray-900 rounded-md p-2 bg-gray-100"
      disabled={!NetowrkMethodsFromTask}
    >
      {NetowrkMethodsFromTask!.map((networkMethod: string) => (
        <option key={networkMethod} value={networkMethod}>
          {networkMethod}
        </option>
      ))}
    </select>
  );
}
