#!/bin/bash
# 1. Dynamically compile the proto file at startup to align namespaces
echo "Compiling inference.proto..."
pip3 install grpcio-tools > /dev/null
python3 -m grpc_tools.protoc \
    -I/app/cmd/compute/edge-compute-yolo \
    --python_out=/app/cmd/compute/edge-compute-yolo \
    --grpc_python_out=/app/cmd/compute/edge-compute-yolo \
    /app/cmd/compute/edge-compute-yolo/inference.proto

# 2. Check and clean install torchvision v0.11.3
python3 -c "import torchvision" 2>/dev/null
if [ $? -ne 0 ]; then
    echo "========================================="
    echo "First-time startup: clean compiling torchvision v0.11.3..."
    echo "========================================="
    # Remove any old dirty torchvision files
    pip3 uninstall -y torchvision
    
    git clone --depth 1 --branch v0.11.3 https://github.com/pytorch/vision.git /tmp/torchvision_src
    cd /tmp/torchvision_src
    export BUILD_VERSION=0.11.3
    # FIXED: Use modern pip install to register package metadata
    python3 -m pip install .
    cd /app
    rm -rf /tmp/torchvision_src
    echo "========================================="
    echo "Torchvision compilation complete!"
    echo "========================================="
fi

# Execute the actual command passed to docker run
exec "$@"
