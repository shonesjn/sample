FROM ubuntu:20.04

# Avoid interactive timezone prompts
ENV DEBIAN_FRONTEND=noninteractive

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 \
    python3-pip \
    g++ \
    libopenblas-base \
    libomp-dev \
    libjpeg-dev \
    zlib1g-dev \
    libgl1-mesa-glx \
    libglib2.0-0 \
    curl \
    git \
    && rm -rf /var/lib/apt/lists/*

# Install python packages using pre-built binary wheels
RUN pip3 install --no-cache-dir numpy "grpcio==1.44.0" protobuf opencv-python-headless gdown
RUN pip3 install --no-cache-dir ultralytics --no-deps

# Download and install the PyTorch GPU wheel
RUN python3 -m gdown 1AQQuBS9skNk1mgZXMp0FmTIwjuxc81WY -O torch-1.11.0-cp38-cp38-linux_aarch64.whl \
    && pip3 install --no-cache-dir torch-1.11.0-cp38-cp38-linux_aarch64.whl \
    && rm torch-1.11.0-cp38-cp38-linux_aarch64.whl

# Copy the entrypoint script to compile torchvision at runtime
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
WORKDIR /app
