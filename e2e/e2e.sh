#!/usr/bin/env bash

set -e

#Always compile ytsync
make

#OVERRIDE this in your .env file if running from mac. Check docker-compose.yml for details
export LOCAL_TMP_DIR="/var/tmp"

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

#ensure that docker can be run and managed from the user
USER=$(whoami)
sudo usermod -aG docker "$USER"

cd ./e2e
docker-compose stop
docker-compose rm -f
echo "$DOCKER_PASSWORD" | docker login --username "$DOCKER_USERNAME" --password-stdin
docker-compose pull
if [[ -d persist ]]; then rm -rf persist; fi
rm -rf persist
mkdir persist
mkdir -p blobsfiles
mkdir persist/.lbrynet
chmod 777 -R ./persist
docker-compose up -d
printf 'waiting for internal apis'
until curl --output /dev/null --silent --head --fail http://localhost:15400; do
    printf '.'
    sleep 1
done
echo "successfully started..."

# make sure we have permission to mess with the volumes
sudo find ./persist -type d -exec chmod 777 {} \;
sudo chown "$USER": -R ./persist

#Data Setup for test
#Add a ytsync user
ADDYTSYNCUSER='INSERT INTO user (given_name) VALUE("ytsync user")'
mysql -u lbry -plbry -D lbry -h "127.0.0.1" -P 15500 -e "$ADDYTSYNCUSER"
#Insert an auth token for the user to be used by ytsync
ADDYTSYNCAUTHTOKEN='INSERT INTO auth_token (user_id, value) VALUE(1,"ytsyntoken")'
mysql -u lbry -plbry -D lbry -h "127.0.0.1" -P 15500 -e "$ADDYTSYNCAUTHTOKEN"
#Give priveledges to ytsync user
ASSIGNGROOP='INSERT INTO user_groop (user_id, groop_id) VALUE( 1,3)'
mysql -u lbry -plbry -D lbry -h "127.0.0.1" -P 15500 -e "$ASSIGNGROOP"

#Add youtuber to sync
ADDYTSYNCER='INSERT INTO user (given_name) VALUE("youtuber")'
mysql -u lbry -plbry -D lbry -h "127.0.0.1" -P 15500 -e "$ADDYTSYNCER"
#Add their youtube channel to be synced
ADDYTCHANNEL="INSERT INTO youtube_data (user_id, status_token,desired_lbry_channel,channel_id,channel_name,status,google_id,google_token,created_at,source,total_videos,total_subscribers)
VALUE(2,'3qzGyuVjQaf7t4pKKu2Er1NRW2LJkeWw','@beamertest','UCCyr5j8akeu9j4Q7urV0Lqw','BeamerAtLBRY','queued',$GOOGLE_ID,'$GOOGLE_TOKEN','2019-08-01 00:00:00','sync',1,0)"
mysql -u lbry -plbry -D lbry -h "127.0.0.1" -P 15500 -e "$ADDYTCHANNEL"


# Execute the test!
./../bin/ytsync --channelID UCCyr5j8akeu9j4Q7urV0Lqw #Force channel intended...just in case. This channel lines up with the api container
# Assert the status
status=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT status FROM youtube_data WHERE id=1')
videoStatus=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT status FROM synced_video WHERE id=1')
if [[ $status != "synced" && $videoStatus != "published" ]]; then
docker-compose logs --tail="all" lbrycrd
docker-compose logs --tail="all" walletserver
docker-compose logs --tail="all" lbrynet
docker-compose logs --tail="all" internalapis
 exit 1; fi;