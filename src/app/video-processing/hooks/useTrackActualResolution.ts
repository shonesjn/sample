import { useEffect, useState } from "react";

export function useTrackActualResolution(rawCameraStream: MediaStream | null) {

  const [actualResolution, setActualResolution] = useState<{
    width: number;
    height: number;
  } | null>(null);

  // Track actual video resolution (mediaDevices.getUserMedia may not respect constraints)
  useEffect(() => {
    if (!rawCameraStream) return;
    const cameraTrack = rawCameraStream.getVideoTracks()[0];
    if (!cameraTrack) return;

    const updateResolution = () => {
      const settings = cameraTrack.getSettings();
      setActualResolution({
        width: settings.width || 0,
        height: settings.height || 0,
      });
    };

    updateResolution();

    const handleSettingsChange = () => updateResolution();
    cameraTrack.addEventListener("settingschange", handleSettingsChange);
    return () => {
      cameraTrack.removeEventListener("settingschange", handleSettingsChange);
    };
  }, [rawCameraStream]);

  return {actualResolution};
}
