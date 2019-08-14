#!/usr/bin/env bash
while true; do
        lbrycrd-cli -conf=/etc/lbry/lbrycrd.conf generate 100 >> /tmp/output.log
        sleep 2
done