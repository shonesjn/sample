import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  typescript: {
    // TODO: Remove this when your project is type safe.
    // !! WARN !!
    // Dangerously allow production builds to successfully complete even if
    // your project has type errors.
    // !! WARN !!
    ignoreBuildErrors: true,
  },
};

export default nextConfig;
