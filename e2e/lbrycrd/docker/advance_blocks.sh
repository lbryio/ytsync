#!/usr/bin/env bash
while true; do
        lbrycrd-cli -conf=/data/.lbrycrd/lbrycrd.conf generate 1 >> /tmp/output.log
        sleep 2
done