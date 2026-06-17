FROM python:3.11.9-slim

ENV PYTHONUNBUFFERED=1 \
    SAM_MODEL_TYPE=vit_b \
    SAM_CHECKPOINT=/models/sam_vit_b_01ec64.pth \
    SAM_DEVICE=cpu \
    SAM_CPU_THREADS=2 \
    SAM_MAX_IMAGE_SIDE=1024 \
    MINIK8S_FUNCTION_PORT=8080

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends git curl ca-certificates libglib2.0-0 libgl1 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir --index-url https://download.pytorch.org/whl/cpu torch torchvision \
    && pip install --no-cache-dir -r /app/requirements.txt

RUN mkdir -p /models \
    && curl -L -o /models/sam_vit_b_01ec64.pth \
       https://dl.fbaipublicfiles.com/segment_anything/sam_vit_b_01ec64.pth

COPY app.py /app/app.py
COPY make_collage.py /app/make_collage.py
COPY image_workflow.py /app/image_workflow.py

EXPOSE 8080
CMD ["python", "/app/app.py"]
