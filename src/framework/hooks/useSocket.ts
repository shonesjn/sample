import { useEffect, useRef, useState } from "react";
import { io, Socket } from "socket.io-client";

export function useSocket() {
  const socketRef = useRef<Socket | null>(null);
  const [isConnected, setIsConnected] = useState(false);
  const [transport, setTransport] = useState("N/A");

  useEffect(() => {
    if (!socketRef.current && typeof window !== "undefined") {
      socketRef.current = io("http://10.1.34.186:3001");
    }

    const socket = socketRef.current;
    if (!socket) return;

    const onConnect = () => {
      setIsConnected(true);
      setTransport(socket.io.engine.transport.name);

      socket.io.engine.on("upgrade", (t) => {
        setTransport(t.name);
      });
    };

    const onDisconnect = () => {
      setIsConnected(false);
      setTransport("N/A");
    };

    socket.on("connect", onConnect);
    socket.on("disconnect", onDisconnect);

    return () => {
      socket.off("connect", onConnect);
      socket.off("disconnect", onDisconnect);
    };
  }, []);

  return {
    socket: socketRef.current,
    isConnected,
    transport,
  };
}
