NVCC ?= nvcc

.PHONY: run clean

run: vector_add
	./vector_add

vector_add: vector_add.cu
	$(NVCC) -O2 vector_add.cu -o vector_add

clean:
	rm -f vector_add
