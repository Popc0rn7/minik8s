#include <cuda_runtime.h>

#include <cmath>
#include <cstdlib>
#include <iostream>
#include <vector>

constexpr int TILE = 16;

#define CUDA_CHECK(call)                                                     \
  do {                                                                       \
    cudaError_t err = (call);                                                \
    if (err != cudaSuccess) {                                                \
      std::cerr << "CUDA error at " << __FILE__ << ":" << __LINE__ << ": " \
                << cudaGetErrorString(err) << "\n";                         \
      std::exit(1);                                                          \
    }                                                                        \
  } while (0)

__global__ void tiledMatMul(const float* a, const float* b, float* c, int n) {
  __shared__ float tileA[TILE][TILE];
  __shared__ float tileB[TILE][TILE];

  int row = blockIdx.y * TILE + threadIdx.y;
  int col = blockIdx.x * TILE + threadIdx.x;
  float acc = 0.0f;

  for (int base = 0; base < n; base += TILE) {
    int aCol = base + threadIdx.x;
    int bRow = base + threadIdx.y;

    tileA[threadIdx.y][threadIdx.x] = (row < n && aCol < n) ? a[row * n + aCol] : 0.0f;
    tileB[threadIdx.y][threadIdx.x] = (bRow < n && col < n) ? b[bRow * n + col] : 0.0f;

    __syncthreads();

    for (int k = 0; k < TILE; ++k) {
      acc += tileA[threadIdx.y][k] * tileB[k][threadIdx.x];
    }

    __syncthreads();
  }

  if (row < n && col < n) {
    c[row * n + col] = acc;
  }
}

float valueA(int row, int col) {
  return static_cast<float>((row + col) % 7) * 0.25f + 1.0f;
}

float valueB(int row, int col) {
  return static_cast<float>((row * 3 + col) % 11) * 0.125f + 0.5f;
}

float expectedAt(const std::vector<float>& a, const std::vector<float>& b, int n, int row, int col) {
  float sum = 0.0f;
  for (int k = 0; k < n; ++k) {
    sum += a[row * n + k] * b[k * n + col];
  }
  return sum;
}

int main() {
  const int n = 1024;
  const dim3 block(TILE, TILE);
  const dim3 grid((n + TILE - 1) / TILE, (n + TILE - 1) / TILE);

  std::vector<float> hostA(n * n);
  std::vector<float> hostB(n * n);
  std::vector<float> hostC(n * n, 0.0f);

  for (int row = 0; row < n; ++row) {
    for (int col = 0; col < n; ++col) {
      hostA[row * n + col] = valueA(row, col);
      hostB[row * n + col] = valueB(row, col);
    }
  }

  float *devA = nullptr, *devB = nullptr, *devC = nullptr;
  CUDA_CHECK(cudaMalloc(&devA, hostA.size() * sizeof(float)));
  CUDA_CHECK(cudaMalloc(&devB, hostB.size() * sizeof(float)));
  CUDA_CHECK(cudaMalloc(&devC, hostC.size() * sizeof(float)));

  CUDA_CHECK(cudaMemcpy(devA, hostA.data(), hostA.size() * sizeof(float), cudaMemcpyHostToDevice));
  CUDA_CHECK(cudaMemcpy(devB, hostB.data(), hostB.size() * sizeof(float), cudaMemcpyHostToDevice));
  CUDA_CHECK(cudaMemset(devC, 0, hostC.size() * sizeof(float)));

  cudaEvent_t start, stop;
  CUDA_CHECK(cudaEventCreate(&start));
  CUDA_CHECK(cudaEventCreate(&stop));
  CUDA_CHECK(cudaEventRecord(start));
  tiledMatMul<<<grid, block>>>(devA, devB, devC, n);
  CUDA_CHECK(cudaGetLastError());
  CUDA_CHECK(cudaEventRecord(stop));
  CUDA_CHECK(cudaEventSynchronize(stop));

  float elapsedMs = 0.0f;
  CUDA_CHECK(cudaEventElapsedTime(&elapsedMs, start, stop));
  CUDA_CHECK(cudaMemcpy(hostC.data(), devC, hostC.size() * sizeof(float), cudaMemcpyDeviceToHost));

  float maxError = 0.0f;
  int checked = 0;
  for (int row = 0; row < n; row += 67) {
    for (int col = 0; col < n; col += 71) {
      float expected = expectedAt(hostA, hostB, n, row, col);
      maxError = std::max(maxError, std::fabs(hostC[row * n + col] - expected));
      ++checked;
    }
  }
  float expectedCorner = expectedAt(hostA, hostB, n, n - 1, n - 1);
  maxError = std::max(maxError, std::fabs(hostC[(n - 1) * n + (n - 1)] - expectedCorner));
  ++checked;

  bool ok = maxError < 1e-2f;

  std::cout << "Matrix N = " << n << "\n";
  std::cout << "Tile size = " << TILE << "\n";
  std::cout << "Block = " << block.x << " x " << block.y << "\n";
  std::cout << "Grid = " << grid.x << " x " << grid.y << "\n";
  std::cout << "Kernel: tiled shared-memory matrix multiplication\n";
  std::cout << "Checked samples = " << checked << "\n";
  std::cout << "Kernel time ms = " << elapsedMs << "\n";
  std::cout << "Max error = " << maxError << "\n";
  std::cout << "Result: " << (ok ? "PASS" : "FAIL") << "\n";

  CUDA_CHECK(cudaEventDestroy(start));
  CUDA_CHECK(cudaEventDestroy(stop));
  CUDA_CHECK(cudaFree(devA));
  CUDA_CHECK(cudaFree(devB));
  CUDA_CHECK(cudaFree(devC));
  return ok ? 0 : 1;
}
