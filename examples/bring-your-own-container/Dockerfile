# SPDX-FileCopyrightText: Copyright (c) 2026 the cauteum authors
# SPDX-License-Identifier: Apache-2.0
#
# Minimal BYOC image — run any Linux container as an cauteum sandbox.
# Image requirements: https://cautem.github.io/cauteum-haven.github.io/reference/images/

FROM python:3.13-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        curl iproute2 \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --gid 1500 app \
    && useradd --uid 1500 --gid app --create-home app \
    && install -d -o app -g app /sandbox /workspace

WORKDIR /sandbox
COPY --chown=app:app app.py .

EXPOSE 8080
USER app

# cauteum replaces ENTRYPOINT/CMD with cauteum-init; pass the start command after --.
CMD ["python", "app.py"]
