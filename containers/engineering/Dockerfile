# syntax=docker/dockerfile:1.7
ARG THREADFORGE_PYTHON_BASE_IMAGE

FROM ${THREADFORGE_PYTHON_BASE_IMAGE} AS wheel-builder
ARG THREADFORGE_PYTHON_BASE_IMAGE
WORKDIR /build
RUN python -c "import os,platform,re,sys; assert sys.version_info[:2] == (3, 12); assert platform.machine() == 'x86_64'; assert re.fullmatch(r'python:3\\.12\\.[0-9]+-slim-bookworm@sha256:[0-9a-f]{64}', os.environ['THREADFORGE_PYTHON_BASE_IMAGE'])"
RUN install -d /build/python
COPY --from=threadforge-python /pyproject.toml /build/python/pyproject.toml
COPY --from=threadforge-python /setup.py /build/python/setup.py
COPY --from=threadforge-python /MANIFEST.in /build/python/MANIFEST.in
COPY --from=threadforge-python /CREDITS.anyjev.md /build/python/CREDITS.anyjev.md
COPY --from=threadforge-python /UPSTREAM_MODIFICATIONS.md /build/python/UPSTREAM_MODIFICATIONS.md
COPY --from=threadforge-python /LICENSE.anyjev /build/python/LICENSE.anyjev
COPY --from=threadforge-python /LICENSE.rrsi /build/python/LICENSE.rrsi
COPY --from=threadforge-python /threadforge_domains /build/python/threadforge_domains
COPY --from=threadforge-python /threadforge_py /build/python/threadforge_py
COPY --from=threadforge-python /anyjev /build/python/anyjev
COPY --from=threadforge-python /rrsi /build/python/rrsi
COPY --from=threadforge-workspace / /build/domains/workspace/
COPY --from=threadforge-archipelago / /build/third_party/archipelago/
COPY build-requirements.lock /build/build-requirements.lock
RUN python -m venv /opt/threadforge-build && \
    /opt/threadforge-build/bin/pip install --no-cache-dir --require-hashes \
      -r /build/build-requirements.lock && \
    /opt/threadforge-build/bin/pip wheel --no-cache-dir --no-deps --no-build-isolation \
      --wheel-dir /wheelhouse /build/python

FROM ${THREADFORGE_PYTHON_BASE_IMAGE} AS runtime
ARG THREADFORGE_PYTHON_BASE_IMAGE
ARG THREADFORGE_REQUIREMENTS_SHA256
ARG THREADFORGE_BUILD_REQUIREMENTS_SHA256
ENV VIRTUAL_ENV=/opt/threadforge/venv \
    PATH=/opt/threadforge/venv/bin:$PATH \
    PYTHONPATH=/opt/threadforge/archipelago \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1
WORKDIR /task
RUN python -c "import platform,sys; assert sys.version_info[:2] == (3, 12); assert platform.machine() == 'x86_64'" && \
    python -m venv "$VIRTUAL_ENV" && \
    install -d /opt/threadforge/archipelago /task && \
    chmod 0555 /opt/threadforge/archipelago /task
RUN python -c "import os,re; assert re.fullmatch(r'[0-9a-f]{64}', os.environ.get('THREADFORGE_REQUIREMENTS_SHA256','')); assert re.fullmatch(r'[0-9a-f]{64}', os.environ.get('THREADFORGE_BUILD_REQUIREMENTS_SHA256','')); assert re.fullmatch(r'python:3\\.12\\.[0-9]+-slim-bookworm@sha256:[0-9a-f]{64}', os.environ['THREADFORGE_PYTHON_BASE_IMAGE'])"
COPY requirements-linux-x86_64-py312.lock /tmp/requirements.lock
RUN /opt/threadforge/venv/bin/pip install --no-cache-dir --require-hashes \
      -r /tmp/requirements.lock
COPY --from=wheel-builder /wheelhouse/ /tmp/wheelhouse/
RUN set -eu; \
    set -- /tmp/wheelhouse/threadforge_python-*.whl; \
    [ "$#" -eq 1 ]; \
    /opt/threadforge/venv/bin/pip install --no-cache-dir --no-deps "$1"; \
    cp -a /opt/threadforge/venv/lib/python3.12/site-packages/threadforge_domains/workspace/runtime/archipelago/. \
      /opt/threadforge/archipelago/; \
    test -f /opt/threadforge/archipelago/runner/agents/models.py; \
    test -f /opt/threadforge/archipelago/harness_eng/main.py; \
    test -f /opt/threadforge/archipelago/LICENSE; \
    test -f /opt/threadforge/venv/lib/python3.12/site-packages/threadforge_domains/workspace/runtime/legal/UPSTREAM_MODIFICATIONS.md; \
    python -c "from fastmcp import FastMCP; import threadforge_domains.engineering.container_runtime; import runner.agents.models; import harness_eng.main"; \
    rm -rf /tmp/wheelhouse /tmp/requirements.lock
RUN chmod 0555 /opt/threadforge
LABEL org.opencontainers.image.title="Threadforge Engineering Runtime" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.base.name="${THREADFORGE_PYTHON_BASE_IMAGE}" \
      org.threadforge.engineering.requirements-sha256="${THREADFORGE_REQUIREMENTS_SHA256}" \
      org.threadforge.engineering.build-requirements-sha256="${THREADFORGE_BUILD_REQUIREMENTS_SHA256}"
USER 65532:65532
CMD ["python", "-m", "threadforge_domains.engineering.container_runtime"]
