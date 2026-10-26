FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 MEM0_TELEMETRY=false \
    ANONYMIZED_TELEMETRY=false TOKENIZERS_PARALLELISM=false \
    OMP_NUM_THREADS=2 FASTEMBED_CACHE_PATH=/models
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
RUN useradd --uid 10001 --create-home context && mkdir /data /models && chown context:context /data /models
USER context
RUN python -c "from fastembed import TextEmbedding; list(TextEmbedding('BAAI/bge-small-zh-v1.5', cache_dir='/models', threads=2).embed(['测试'])); from llama_index.core.node_parser import SentenceSplitter; SentenceSplitter(chunk_size=384, chunk_overlap=48).split_text('文档验证')"
COPY *.py .
EXPOSE 8580
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8580", "--no-access-log"]
