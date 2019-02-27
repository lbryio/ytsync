#!/usr/bin/env bash

GO111MODULE=off go get github.com/caarlos0/svu
git tag `svu "$1"`
git push --tags
