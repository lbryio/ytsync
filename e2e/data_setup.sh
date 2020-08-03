#!/usr/bin/env bash

set -e

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
#Insert an auth token for the youtuber to be used by ytsync
ADDYTSYNCAUTHTOKEN='INSERT INTO auth_token (user_id, value) VALUE(2,"youtubertoken")'
mysql -u lbry -plbry -D lbry -h "127.0.0.1" -P 15500 -e "$ADDYTSYNCAUTHTOKEN"
#Add their youtube channel to be synced
ADDYTCHANNEL="INSERT INTO youtube_data (user_id, status_token,desired_lbry_channel,channel_id,channel_name,status,created_at,source,total_videos,total_subscribers,should_sync,redeemable,total_views,reviewed,last_uploaded_video)
VALUE(2,'3qzGyuVjQaf7t4pKKu2Er1NRW2LJkeWw','@beamertest','UCNQfQvFMPnInwsU_iGYArJQ','BeamerAtLBRY','queued','2019-08-01 00:00:00','sync',10,10,1,1,10000,1,'7bBV2Z-9wpo')"
mysql -u lbry -plbry -D lbry -h "127.0.0.1" -P 15500 -e "$ADDYTCHANNEL"
