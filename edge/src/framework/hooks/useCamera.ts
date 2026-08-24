import { useCallback, useState } from "react";

export function useCamera() {
  const [cameraStream, setCameraStream] = useState<MediaStream | null>(null);
  const [cameraTrack, setCameraTrack] = useState<MediaStreamTrack | null>(null);

  const startCamera = useCallback(
    async (cameraWidth: number, cameraHeight: number, cameraFps: number) => {
      try {
        const mediaStreamConstraints = {
          video: {
            width: { ideal: cameraWidth },
            height: { ideal: cameraHeight },
            frameRate: { ideal: cameraFps },
          },
        };

        const mediaStream = await navigator.mediaDevices.getUserMedia(
          mediaStreamConstraints,
        );

        const videoTrack = mediaStream.getVideoTracks()[0];

        setCameraTrack(videoTrack);
        setCameraStream(mediaStream);
      } catch (error) {
        console.error("Error starting camera:", error);
        throw error;
      }
    },
    [],
  );

  const stopCamera = useCallback(() => {
    cameraStream?.getTracks().forEach((track) => track.stop());
    cameraTrack?.stop();
    setCameraStream(null);
    setCameraTrack(null);
  }, [cameraStream, cameraTrack]);

  return {
    cameraStream,
    cameraTrack,
    startCamera,
    stopCamera,
  };
}
