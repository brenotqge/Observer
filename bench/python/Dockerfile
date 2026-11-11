# Imagem mínima equivalente para executar a versão Python (apenas para
# comparar tamanho de imagem com a do Observer em Go).
FROM python:3.13-slim
COPY aggregate.py /app/aggregate.py
USER nobody
ENTRYPOINT ["python3", "/app/aggregate.py"]
