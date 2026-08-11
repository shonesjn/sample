import { useEffect, useRef } from "react";

export const useMediaStreamToCanvasRef = (mediaStream: MediaStream | null) => {
  const localCanvasRef = useRef<HTMLCanvasElement>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);

  useEffect(() => {
    if (!mediaStream || !localCanvasRef.current) return;

    const canvas = localCanvasRef.current;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    // Create video element only once and store in ref
    if (!videoRef.current) {
      videoRef.current = document.createElement("video");
      videoRef.current.muted = true;
      videoRef.current.playsInline = true;
      videoRef.current.autoplay = true;
    }

    const video = videoRef.current;
    video.srcObject = mediaStream;

    let animationFrameId: number;
    const draw = () => {
      if (video.readyState >= video.HAVE_CURRENT_DATA) {
        if (
          canvas.width !== video.videoWidth ||
          canvas.height !== video.videoHeight
        ) {
          canvas.width = video.videoWidth;
          canvas.height = video.videoHeight;
        }
        ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
      }
      animationFrameId = requestAnimationFrame(draw);
    };

    const handleLoadedMetadata = () => {
      video.play().then(() => {
        draw();
      }).catch((error) => {
        console.error("Video play failed in Raw stream to canvas:", error);
      });
    };

    // Wait for metadata to load before playing
    // video.play() can fail if called before metadata is loaded
    if (video.readyState >= video.HAVE_METADATA) {
      handleLoadedMetadata();
    } else {
      video.addEventListener("loadedmetadata", handleLoadedMetadata);
    }

    return () => {
      video.removeEventListener("loadedmetadata", handleLoadedMetadata);
      cancelAnimationFrame(animationFrameId);
      video.pause();
      video.srcObject = null;
    };
  }, [mediaStream]);

  return localCanvasRef;
};
