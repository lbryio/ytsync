#!/usr/bin/env bash

set -e

#Always compile ytsync
make
#Always compile supporty
cd e2e/supporty && make && cd ../..

#OVERRIDE this in your .env file if running from mac. Check docker-compose.yml for details
export LOCAL_TMP_DIR="/var/tmp:/var/tmp"

#Private Variables Set in local installations: SLACK_TOKEN,YOUTUBE_API_KEY,AWS_S3_ID,AWS_S3_SECRET,AWS_S3_REGION,AWS_S3_BUCKET
touch -a .env && set -o allexport; source ./.env; set +o allexport
echo "LOCAL_TMP_DIR=$LOCAL_TMP_DIR"
# Compose settings - docker only
export LBRYNET_ADDRESS="http://localhost:15100"
export LBRYCRD_STRING="tcp://lbry:lbry@localhost:15200" #required for supporty
export LBRYNET_USE_DOCKER=true
export REFLECT_BLOBS=false
export CLEAN_ON_STARTUP=true
export REGTEST=true
# Local settings
export BLOBS_DIRECTORY="$(pwd)/e2e/blobsfiles"
export LBRYNET_DIR="$(pwd)/e2e/persist/.lbrynet/.local/share/lbry/lbrynet/"
export LBRYUM_DIR="$(pwd)/e2e/persist/.lbrynet/.local/share/lbry/lbryum"
export TMP_DIR="/var/tmp"
export CHAINNAME="lbrycrd_regtest"
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

channelToSync="UCMn-zv1SE-2y6vyewscfFqw"
channelName=@whatever"$(date +%s)"
latestVideoID="yPJgjiMbmX0"

#Data Setup for test
./data_setup.sh "$channelName" "$channelToSync" "$latestVideoID"

# Execute the sync test!
./../bin/ytsync --channelID "$channelToSync" --videos-limit 2 --concurrent-jobs 4 --quick #Force channel intended...just in case. This channel lines up with the api container
status=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT status FROM youtube_data WHERE id=1')
videoStatus=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT status FROM synced_video WHERE id=1')
videoClaimID1=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT publish.claim_id FROM synced_video INNER JOIN publish ON publish.id = synced_video.publish_id WHERE synced_video.id=1')
videoClaimID2=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT publish.claim_id FROM synced_video INNER JOIN publish ON publish.id = synced_video.publish_id WHERE synced_video.id=2')
videoClaimAddress1=$(mysql -u lbry -plbry -ss -D chainquery -h "127.0.0.1" -P 15500 -e 'SELECT claim_address FROM claim WHERE id=2')
videoClaimAddress2=$(mysql -u lbry -plbry -ss -D chainquery -h "127.0.0.1" -P 15500 -e 'SELECT claim_address FROM claim WHERE id=3')
# Create Supports for published claim
./supporty/supporty "$channelName" "${videoClaimID1}" "${videoClaimAddress1}" lbrycrd_regtest 1.0
./supporty/supporty "$channelName" "${videoClaimID2}" "${videoClaimAddress2}" lbrycrd_regtest 2.0
./supporty/supporty "$channelName" "${videoClaimID2}" "${videoClaimAddress2}" lbrycrd_regtest 3.0
./supporty/supporty "$channelName" "${videoClaimID1}" "${videoClaimAddress1}" lbrycrd_regtest 3.0
curl --data-binary '{"jsonrpc":"1.0","id":"curltext","method":"generate","params":[1]}' -H 'content-type:text/plain;' --user lbry:lbry http://localhost:15200
# Reset status for transfer test
mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e "UPDATE youtube_data SET status = 'queued' WHERE id = 1"
# Trigger transfer api
curl -i -H 'Accept: application/json' -H 'Content-Type: application/json' 'http://localhost:15400/yt/transfer?auth_token=youtubertoken&address=n4eYeXAYmHo4YRUDEfsEhucy8y5LKRMcHg&public_key=tpubDA9GDAntyJu4hD3wU7175p7CuV6DWbYXfyb2HedBA3yuBp9HZ4n3QE4Ex6RHCSiEuVp2nKAL1Lzf2ZLo9ApaFgNaJjG6Xo1wB3iEeVbrDZp'
# Execute the transfer test!
./../bin/ytsync --channelID $channelToSync --videos-limit 2 --concurrent-jobs 4 --quick #Force channel intended...just in case. This channel lines up with the api container
# Check that the channel and the video are marked as transferred and that all supports are spent
channelTransferStatus=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT distinct transfer_state FROM youtube_data')
videoTransferStatus=$(mysql -u lbry -plbry -ss -D lbry -h "127.0.0.1" -P 15500 -e 'SELECT distinct transferred FROM synced_video')
nrUnspentSupports=$(mysql -u lbry -plbry -ss -D chainquery -h "127.0.0.1" -P 15500 -e 'SELECT COUNT(*) FROM chainquery.support INNER JOIN output ON output.transaction_hash = support.transaction_hash_id AND output.vout = support.vout WHERE output.is_spent = 0')
if [[ $status != "synced" || $videoStatus != "published" || $channelTransferStatus != "2" || $videoTransferStatus != "1" || $nrUnspentSupports != "1" ]]; then
    echo "~~!!!~~~FAILED~~~!!!~~"
    echo "Channel Status: $status"
    echo "Video Status: $videoStatus"
    echo "Channel Transfer Status: $channelTransferStatus"
    echo "Video Transfer Status: $videoTransferStatus"
    echo "Nr Unspent Supports: $nrUnspentSupports"
    #docker-compose logs --tail="all" lbrycrd
    #docker-compose logs --tail="all" walletserver
    #docker-compose logs --tail="all" lbrynet
    #docker-compose logs --tail="all" internalapis
    exit 1;
    else
      echo "SUCCESSSSSSSSSSSSS!"
fi;
docker-compose down