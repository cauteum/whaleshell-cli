# SPDX-FileCopyrightText: Copyright (c) 2026 the cautem authors
# SPDX-License-Identifier: Apache-2.0
#
# Minimal BYOC image — run any Linux container as an cautem sandbox.
# Image requirements: https://cautem.github.io/sandbox.dev/reference/images/

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

# cautem replaces ENTRYPOINT/CMD with cautem-init; pass the start command after --.
CMD ["python", "app.py"]
