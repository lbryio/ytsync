#!/bin/bash
if [ $# -eq 0 ]
  then
    echo "No docker tag argument supplied. Use './build.sh <tag>'"
    exit 1
fi
docker build --tag lbry/lbrycrd:$1 .
docker push lbry/lbrycrd:$1