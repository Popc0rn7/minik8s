#include <cuda_runtime.h>

#include <cmath>
#include <iostream>
#include <vector>

__global__ void vectorAdd(const float* a, const float* b, float* c, int n) {
  int i = blockIdx.x * blockDim.x + threadIdx.x;
  if (i < n) {
    c[i] = a[i] + b[i];
  }
}

int main() {
  const int n = 1 << 20;
  const int threadsPerBlock = 256;
  const int blocksPerGrid = (n + threadsPerBlock - 1) / threadsPerBlock;

  std::vector<float> hostA(n, 1.5f);
  std::vector<float> hostB(n, 2.5f);
  std::vector<float> hostC(n, 0.0f);

  float *devA = nullptr, *devB = nullptr, *devC = nullptr;
  cudaMalloc(&devA, n * sizeof(float));
  cudaMalloc(&devB, n * sizeof(float));
  cudaMalloc(&devC, n * sizeof(float));

  cudaMemcpy(devA, hostA.data(), n * sizeof(float), cudaMemcpyHostToDevice);
  cudaMemcpy(devB, hostB.data(), n * sizeof(float), cudaMemcpyHostToDevice);

  vectorAdd<<<blocksPerGrid, threadsPerBlock>>>(devA, devB, devC, n);
  cudaDeviceSynchronize();

  cudaMemcpy(hostC.data(), devC, n * sizeof(float), cudaMemcpyDeviceToHost);

  bool ok = true;
  for (int i = 0; i < n; ++i) {
    if (std::fabs(hostC[i] - 4.0f) > 1e-5f) {
      ok = false;
      break;
    }
  }

  std::cout << "N = " << n << "\n";
  std::cout << "threadsPerBlock = " << threadsPerBlock << "\n";
  std::cout << "blocksPerGrid = " << blocksPerGrid << "\n";
  std::cout << "Result: " << (ok ? "PASS" : "FAIL") << "\n";

  cudaFree(devA);
  cudaFree(devB);
  cudaFree(devC);
  return ok ? 0 : 1;
}
