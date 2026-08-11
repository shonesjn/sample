"use client";
// TODO: Make this a server component
import Link from "next/link";
import { Task } from "@framework/definitions";
import { useQuery } from "@tanstack/react-query";

export default function Sidebar() {
  const {
    data: tasks,
    isLoading,
    isError,
  } = useQuery<Task[]>({
    queryKey: ["sidebar"],
    queryFn: () => fetch(`/api/tasks`).then((res) => res.json()),
  });

  if (isLoading) {
    return <div>Loading tasks...</div>;
  }
  if (isError) {
    return <div>Error loading tasks</div>;
  }

  return (
    <div className="w-64 h-screen bg-gray-800 text-white p-4 flex flex-col">
      <Link
        href="/"
        className="flex items-center space-x-2 p-3 rounded-lg hover:bg-gray-700
        mb-4"
      >
        {/*TODO: ICON*/}
        <span>Home</span>
      </Link>

      {/* Tasks Section */}
      <div className="flex-1 overflow-y-auto">
        <h3 className="text-xs uppercase tracking-wider text-gray-400 mb-2 px-3">
          Tasks
        </h3>
        <div className="space-y-1">
          {tasks?.map((task) => (
            <Link
              key={task.taskID}
              href={`/${task.taskID}`}
              className="w-full text-left p-3 rounded-lg hover:bg-gray-700 flex items-center"
            >
              <span className="truncate">{task.name}</span>
            </Link>
          ))}
        </div>
      </div>
    </div>
  );
}
