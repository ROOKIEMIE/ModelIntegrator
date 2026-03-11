FROM alpine:3.20

RUN apk add --no-cache bash curl jq coreutils

WORKDIR /workspace
COPY testsystem/scenarios /workspace/testsystem/scenarios
COPY testsystem/scripts /workspace/testsystem/scripts

ENTRYPOINT ["/bin/bash", "/workspace/testsystem/scripts/run_test.sh"]
