#!/bin/bash
lbrynet start \
  --api="${API_BIND_IP:-0.0.0.0}":"${API_PORT:-5279}" \
  --config="${CONFIG_PATH:-/etc/lbry/daemon_settings.yml}"