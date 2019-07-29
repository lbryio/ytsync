FROM ubuntu:18.04 as prep
LABEL MAINTAINER="leopere [at] nixc [dot] us"
## TODO: Implement version pinning. `apt-get install curl=<version>`
RUN apt-get update && \
  apt-get -y install unzip curl build-essential && \
  apt-get autoclean -y && \
  rm -rf /var/lib/apt/lists/*
WORKDIR /
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
COPY ./start.sh start
COPY ./healthcheck.sh healthcheck
COPY ./advance_blocks.sh advance
COPY ./fix-permissions.c fix-permissions.c

## Add lbrycrd - Change the version below to create an image for a different tag/version
ARG VERSION="v0.12.4.1"
RUN URL=$(curl -s https://api.github.com/repos/lbryio/lbrycrd/releases/$(if [ "${VERSION}" = 'latest' ]; then echo "latest"; else echo "tags/${VERSION}"; fi) | grep browser_download_url | grep lbrycrd-linux.zip | cut -d'"' -f4) && echo $URL && curl -L -o /lbrycrd-linux.zip $URL

RUN unzip ./lbrycrd-linux.zip && \
  gcc fix-permissions.c -o fix-permissions && \
  chmod +x ./lbrycrdd ./lbrycrd-cli ./lbrycrd-tx ./start ./healthcheck ./fix-permissions ./advance

FROM ubuntu:18.04 as app
COPY --from=prep /lbrycrdd /lbrycrd-cli /lbrycrd-tx /start /healthcheck /fix-permissions /advance /usr/bin/
RUN addgroup --gid 1000 lbrycrd && \
  adduser lbrycrd --uid 1000 --gid 1000 --gecos GECOS --shell /bin/bash --disabled-password --home /data && \
  mkdir /etc/lbry && \
  chown lbrycrd /etc/lbry && \
  chmod a+s /usr/bin/fix-permissions
VOLUME ["/data"]
WORKDIR /data
## TODO: Implement healthcheck.
# HEALTHCHECK ["healthcheck"]
EXPOSE 9246 9245 11337 29245

USER lbrycrd
CMD ["start"]