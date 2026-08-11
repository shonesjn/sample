import { useMemo } from "react";
import { useWebRTCTransport } from "./useWebRTCTransport";
import { useWebSocketTransport } from "./useWebsocketTransport";
import {
  OffloadTransport,
  TransportMethods,
} from "./types";

type TransportResult = {
  pc: RTCPeerConnection | null;
  offloadTransport: OffloadTransport;
};

function useTransport(transportMethod: TransportMethods): TransportResult {
  // React Hooks must be called unconditionally
  const webRTCTransport = useWebRTCTransport();
  const webSocketTransport = useWebSocketTransport();

  return useMemo(() => {
    if (transportMethod === "webrtc") {
      return webRTCTransport;
    } else if (transportMethod === "websocket") {
      return {
        pc: null,
        offloadTransport: webSocketTransport.offloadTransport,
      };
    } else {
      return {
        pc: null,
        offloadTransport: {
          connect: async () => console.warn("No transport selected."),
          disconnect: () => console.warn("No transport selected."),
          sendFrame: () => console.warn("No transport selected."),
          onDataReceived: () => console.warn("No transport selected."),
          connectionState: "disconnected" as const,
          transportMethod: "none" as const,
        },
      };
    }
  }, [transportMethod, webRTCTransport, webSocketTransport]);
}

export default useTransport;