export const resize = (
  imageData: ImageData,
  width: number,
  height: number,
): ImageData => {
  const canvas = new OffscreenCanvas(width, height);
  const ctx = canvas.getContext("2d");

  if (!ctx) {
    throw new Error("Could not get 2d context");
  }

  ctx.putImageData(imageData, 0, 0);
  return ctx.getImageData(0, 0, width, height);
};

// TODO: Implement better kernel applying logic
export function applyKernel(imageData: ImageData, kernel: number[]): ImageData {
  const data = imageData.data;
  const width = imageData.width;
  const height = imageData.height;

  const tempData = new Uint8ClampedArray(data.length);

  for (let y = 1; y < height - 1; y++) {
    for (let x = 1; x < width - 1; x++) {
      let r = 0,
        g = 0,
        b = 0;

      for (let ky = -1; ky <= 1; ky++) {
        for (let kx = -1; kx <= 1; kx++) {
          const pixelIndex = ((y + ky) * width + (x + kx)) * 4;
          const kernelIndex = (ky + 1) * 3 + (kx + 1);

          r += data[pixelIndex] * kernel[kernelIndex];
          g += data[pixelIndex + 1] * kernel[kernelIndex];
          b += data[pixelIndex + 2] * kernel[kernelIndex];
        }
      }

      const outputIndex = (y * width + x) * 4;
      tempData[outputIndex] = r;
      tempData[outputIndex + 1] = g;
      tempData[outputIndex + 2] = b;
      tempData[outputIndex + 3] = data[outputIndex + 3]; // Preserve alpha
    }
  }

  return new ImageData(tempData, width, height);
}
