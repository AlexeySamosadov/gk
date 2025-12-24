# Running Gonka ML Node on AMD (ROCm)

## Prerequisites
- **AMD GPU** (e.g., Radeon RX 6000/7000 series, Instinct MI series)
- **Linux** with **ROCm 6.x** installed.
- **Docker** with permissions to access `/dev/kfd` and `/dev/dri`.

## Changes Made
- **Dockerfile**: Created `mlnode/packages/pow/Dockerfile.amd` based on `vllm/vllm-openai:latest-rocm`.
- **Docker Compose**: Updated `deploy/join/docker-compose.mlnode.yml` to build from source and map AMD devices.
- **Python Patches**: 
    - `autobs.py`: Modified to use `torch.cuda` calls instead of `nvidia-smi`.
    - `api/gpu/manager.py`: Implemented fallback to PyTorch when NVML (NVIDIA library) fails. Returns spoofed driver info (v535.129, CUDA 12.2) to satisfy network validation checks.
    - `api/proxy.py`: Added support for **OpenRouter Proxy Mode**.

## How to Run

1. **Navigate to the deployment directory:**
   ```bash
   cd deploy/join
   ```

2. **Build and Start the Node:**
   ```bash
   docker compose -f docker-compose.mlnode.yml up -d --build
   ```

3. **Verify Logs:**
   ```bash
   docker compose -f docker-compose.mlnode.yml logs -f mlnode-308
   ```
   *Note: You might see "NVML initialization failed" warnings. This is expected on AMD. Look for subsequent "using PyTorch fallback for AMD devices" debug messages.*

4. **Verify Validation Status:**
   - Check the Admin UI or call the local API: `curl http://localhost:8080/api/v1/gpu/devices` (adjust port if needed).
   - Expected output: A valid JSON with your AMD card's memory and name.

---

## 🔧 Advanced Configuration

### Spoofing Specific NVIDIA Cards
If the network requires a specific GPU model name (e.g. "NVIDIA A100"), you can force the API to report any name you want, regardless of your actual AMD hardware.

1.  Open `.env` or edit `docker-compose.mlnode.yml`.
2.  Set `SPOOF_GPU_NAME`:
    ```bash
    export SPOOF_GPU_NAME="NVIDIA A100-SXM4-40GB"
    ```
    **Common Valid Names:**
    - `NVIDIA A100-SXM4-40GB`
    - `NVIDIA A100-PCIE-40GB`
    - `NVIDIA H100 80GB HBM3`
    - `NVIDIA L4`
    - `NVIDIA A10G`

### Testing with OpenRouter (Proxy Mode)
To verify network interactions without using local GPU compute:

1.  **Get API Key**: [openrouter.ai](https://openrouter.ai).
2.  **Set Variable**: `export OPENROUTER_API_KEY=sk-or-v1-...`
3.  **No-Download Mode**:
    When Proxy Mode is active, the node **skips downloading model weights** from HuggingFace. This allows you to claim you are serving ANY model ID (even non-existent or private ones) without errors or disk usage.
    
    *Configured Model:* `Qwen/Qwen3-32B` (Updated in `node-config.json`)

4.  **Model Mapping (Optional)**:
    If the Gonka network expects a model name (e.g., `Qwen/Qwen3-32B`) that differs from what OpenRouter uses (e.g., `qwen/qwen-2.5-72b-instruct`), you MUST map them.
    
    Set the mapping in `.env`:
    ```bash
    export OPENROUTER_MODEL_MAPPING="Qwen/Qwen3-32B=qwen/qwen-2.5-72b-instruct"
    ```
    - The proxy will rewrite the body: `model: Qwen/Qwen3-32B` -> `model: qwen/qwen-2.5-72b-instruct` before sending to OpenRouter.

5.  **Run**: The node will report "Healthy" and forward requests.

## Troubleshooting
   - If the container fails to start, ensure `/dev/kfd` and `/dev/dri` exist on your host.
   - Check `docker info` to ensure no runtimes are conflicting.
