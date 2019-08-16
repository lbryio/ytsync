#!/usr/bin/env bash

set -e

#Always compile ytsync
make

#OVERRIDE this in your .env file if running from mac. Check docker-compose.yml for details
export LOCAL_TMP_DIR="/var/tmp:/var/tmp"

#Private Variables Set in local installations: SLACK_TOKEN,YOUTUBE_API_KEY,AWS_S3_ID,AWS_S3_SECRET,AWS_S3_REGION,AWS_S3_BUCKET
touch -a .env && set -o allexport; source ./.env; set +o allexport
echo "LOCAL_TMP_DIR=$LOCAL_TMP_DIR"
# Compose settings - docker only
export SLACK_CHANNEL="ytsync-travis"
export LBRY_API_TOKEN="ytsyntoken"
export LBRY_WEB_API="http://localhost:15400"
export LBRYNET_ADDRESS="http://localhost:15100"
export LBRYCRD_STRING="tcp://lbry:lbry@localhost:15200"
export LBRYNET_USE_DOCKER=true
export REFLECT_BLOBS=false
export CLEAN_ON_STARTUP=true
export REGTEST=true
# Local settings
export BLOBS_DIRECTORY="$(pwd)/e2e/blobsfiles"
export LBRYNET_DIR="$(pwd)/e2e/persist/.lbrynet/.local/share/lbry/lbrynet/"
export LBRYNET_WALLETS_DIR="$(pwd)/e2e/persist/.lbrynet/.local/share/lbry/lbryum"
export TMP_DIR="/var/tmp"
export UID

cd ./e2e
docker-compose stop
docker-compose rm -f
echo "$DOCKER_PASSWORD" | docker login --username "$DOCKER_USERNAME" --password-stdin
docker-compose pull
if [[ -d persist ]]; then rm -rf persist; fi
mkdir  -m 0777 -p ./persist
mkdir -m 777 -p ./persist/.walletserver
mkdir -m 777 -p ./persist/.lbrynet
#sudo chown -Rv 999:999 ./persist/.walletserver
#sudo chown -Rv 1000:1000 ./persist/.lbrynet
docker-compose up -d
printf 'waiting for internal apis'
until curl --output /dev/null --silent --head --fail http://localhost:15400; do
    printf '.'
    sleep 1
done
echo "successfully started..."

#Data Setup for test
./data_setup.sh

# Execute the sync test!
./../bin/ytsync --channelID UCCyr5j8akeu9j4Q7urV0Lqw #Force channel intended...just in case. This channel lines up with the api container
status=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT status FROM youtube_data WHERE id=1')
videoStatus=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT status FROM synced_video WHERE id=1')
# Reset status for tranfer test
mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e "UPDATE youtube_data SET status = 'queued' WHERE id = 1"
# Trigger transfer api
echo "curl -i -H 'Accept: application/json' -H 'Content-Type: application/json' http://localhost:15400/yt/transfer?auth_token=youtubertoken&address=n1Ygra2pyD6cpESv9GtPM9kDkr4bPeu1Dc"
# Execute the transfer test!
./../bin/ytsync --channelID UCCyr5j8akeu9j4Q7urV0Lqw #Force channel intended...just in case. This channel lines up with the api container
transferStatus=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT transferred FROM youtube_data WHERE id=1')
if [[ $status != "synced" || $videoStatus != "published" || transferStatus != "1" ]]; then
echo "Channel Status: $status"
echo "Video Status: $videoStatus"
echo "Transfer Status: $transferStatus"
#docker-compose logs --tail="all" lbrycrd
#docker-compose logs --tail="all" walletserver
#docker-compose logs --tail="all" lbrynet
#docker-compose logs --tail="all" internalapis
echo "List local /var/tmp"
find /var/tmp
 exit 1; fi;