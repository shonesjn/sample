export interface OffloadTransport {
  /**
   * Initiates a connection to the edge server.
   * @param config - Configuration object, e.g., signaling server URL.
   */
  connect: (config: { serverUrl: string }) => Promise<void>;

  /**
   * Terminates the connection.
   */
  disconnect: () => void;

 /**
   * Sends a single video frame to the edge.
   * The transport is responsible for handling the canvas appropriately.
   * @param frameCanvas - The canvas element containing the video frame to be sent.
   */
  sendFrame: (frameCanvas: HTMLCanvasElement) => void;

  /**
   * A way to register a callback function that will be invoked
   * whenever processed data is received from the edge.
   * @param callback - The function to call with the incoming data.
   */
  onDataReceived: (callback: (data: any) => void) => void;

  /**
   * The current state of the connection, for UI feedback.
   */
  connectionState: 'disconnected' | 'connecting' | 'connected' | 'failed';

  /** Indicate the transport method used
   */
  transportMethod: TransportMethods;
}

export interface WebRTCTransport {
  pc: RTCPeerConnection | null;
  offloadTransport: OffloadTransport;
}

export interface WebSocketTransport {
  socket: WebSocket | null;
  offloadTransport: OffloadTransport;
}

export type TransportMethods = 'webrtc' | 'websocket' | 'none';
