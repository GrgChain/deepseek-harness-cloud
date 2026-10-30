FROM quay.io/docling-project/docling-serve-cpu:v1.36.0@sha256:225c8586e20d5d0fc6811a9e0e044fa602bcc4393f00389009bad42d6787b58f

USER 0
RUN dnf install -y libreoffice-writer libreoffice-calc libreoffice-impress && dnf clean all
USER 1001
