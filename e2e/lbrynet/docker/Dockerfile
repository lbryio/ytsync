## This base image is for running the latest lbrynet-daemon release.
FROM ubuntu:18.04 as prep
LABEL MAINTAINER="leopere [at] nixc [dot] us"
RUN apt-get update && apt-get -y install unzip curl telnet wait-for-it

## Add lbrynet
ARG VERSION="latest"
RUN URL=$(curl -s https://api.github.com/repos/lbryio/lbry-sdk/releases/$(if [ "${VERSION}" = 'latest' ]; then echo "latest"; else echo "tags/${VERSION}"; fi) | grep browser_download_url | grep lbrynet-linux.zip | cut -d'"' -f4) && echo $URL && curl -L -o /lbrynet.linux.zip $URL

COPY start.sh /usr/bin/start
COPY checkmount.sh /usr/bin/checkmount
RUN unzip /lbrynet.linux.zip -d /lbrynet/ && \
    mv /lbrynet/lbrynet /usr/bin && \
    chmod a+x /usr/bin/checkmount /usr/bin/start /usr/bin/lbrynet

FROM ubuntu:18.04 as app
COPY --from=prep /usr/bin/start /usr/bin/checkmount /usr/bin/lbrynet /usr/bin/
RUN adduser lbrynet --gecos GECOS --shell /bin/bash --disabled-password --home /home/lbrynet
## Daemon port [Intended for internal use]
## LBRYNET talks to peers on port 3333 [Intended for external use] this port is used to discover other lbrynet daemons with blobs.
## Expose 5566 Reflector port to listen on
## Expose 5279 Port the daemon API will listen on
## the lbryumx aka Wallet port [Intended for internal use]
#EXPOSE 4444 3333 5566 5279 50001
USER lbrynet
ENTRYPOINT ["/usr/bin/checkmount"]
CMD ["start"]