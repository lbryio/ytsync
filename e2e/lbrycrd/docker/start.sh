#!/usr/bin/env bash
CONFIG_PATH=/etc/lbry/lbrycrd.conf

function override_config_option() {
    # Remove existing config line from a config file
    #  and replace with environment fed value.
    # Does nothing if the variable does not exist.
    #  var     Name of ENV variable
    #  option  Name of config option
    #  config  Path of config file
    local var=$1 option=$2 config=$3
    if [[ -v $var ]]; then
        # Remove the existing config option:
        sed -i "/^$option\W*=/d" "$config"
        # Add the value from the environment:
        echo "$option=${!var}" >> "$config"
    fi
}

function set_config() {
  if [ -d "$CONFIG_PATH" ]; then
      echo "$CONFIG_PATH is a directory when it should be a file."
      exit 1
  elif [ -f "$CONFIG_PATH" ]; then
      echo "Merging the mounted config file with environment variables."
      local MERGED_CONFIG=/tmp/lbrycrd_merged.conf
      cat $CONFIG_PATH > $MERGED_CONFIG
      echo "" >> $MERGED_CONFIG
      override_config_option PORT port $MERGED_CONFIG
      override_config_option RPC_USER rpcuser $MERGED_CONFIG
      override_config_option RPC_PASSWORD rpcpassword $MERGED_CONFIG
      override_config_option RPC_ALLOW_IP rpcallowip $MERGED_CONFIG
      override_config_option RPC_PORT rpcport $MERGED_CONFIG
      override_config_option RPC_BIND rpcbind $MERGED_CONFIG
      # Make the new merged config file the new CONFIG_PATH
      # This ensures that the original file the user mounted remains unmodified
      CONFIG_PATH=$MERGED_CONFIG
  else
      echo "Creating a fresh config file from environment variables."
      ## Set config params
      {
        echo "port=${PORT=9246}"
        echo "rpcuser=${RPC_USER=lbry}"
        echo "rpcpassword=${RPC_PASSWORD=lbry}"
        echo "rpcallowip=${RPC_ALLOW_IP=127.0.0.1/24}"
        echo "rpcport=${RPC_PORT=9245}"
        echo "rpcbind=${RPC_BIND=0.0.0.0}"
        echo "deprecatedrpc=accounts"
        echo "deprecatedrpc=validateaddress"
        echo "deprecatedrpc=signrawtransaction"
      } >>  $CONFIG_PATH
  fi
  echo "Config: "
  cat $CONFIG_PATH
}

## Ensure perms are correct prior to running main binary
/usr/bin/fix-permissions

## You can optionally specify a run mode if you want to use lbry defined presets for compatibility.
case $RUN_MODE in
  default )
    set_config
    lbrycrdd -server -conf=$CONFIG_PATH -printtoconsole
    ;;
  ## If it's a first run you need to do a full index including all transactions
  ## tx index creates an index of every single transaction in the block history if
  ## not specified it will only create an index for transactions that are related to the wallet or have unspent outputs.
  ## This is generally specific to chainquery.
  reindex )
    ## Apply this RUN_MODE in the case you need to update a dataset.  NOTE: you do not need to use `RUN_MODE reindex` for more than one complete run.
    set_config
    lbrycrdd -server -txindex -reindex -conf=$CONFIG_PATH -printtoconsole
    ;;
  chainquery )
    ## If your only goal is to run Chainquery against this instance of lbrycrd and you're starting a
    ## fresh local dataset use this run mode.
    set_config
    lbrycrdd -server -txindex -conf=$CONFIG_PATH -printtoconsole
    ;;
  regtest )
    ## Set config params
    ## TODO: Make this more automagic in the future.
    mkdir -p "$(dirname $CONFIG_PATH)"
    echo "rpcuser=lbry" >                       $CONFIG_PATH
    echo "rpcpassword=lbry" >>                  $CONFIG_PATH
    echo "rpcport=29245" >>                     $CONFIG_PATH
    echo "rpcbind=0.0.0.0" >>                   $CONFIG_PATH
    echo "rpcallowip=0.0.0.0/0" >>              $CONFIG_PATH
    echo "regtest=1" >>                         $CONFIG_PATH
    echo "txindex=1" >>                         $CONFIG_PATH
    echo "server=1" >>                          $CONFIG_PATH
    echo "printtoconsole=1" >>                  $CONFIG_PATH
    echo "deprecatedrpc=accounts" >>            $CONFIG_PATH
    echo "deprecatedrpc=validateaddress" >>     $CONFIG_PATH
    echo "deprecatedrpc=signrawtransaction" >>  $CONFIG_PATH
    echo "vbparams=segwit:0:999999999999" >>    $CONFIG_PATH
    echo "addresstype=legacy" >>                $CONFIG_PATH

    #nohup advance &>/dev/null &
    lbrycrdd -conf=$CONFIG_PATH $1
    ;;
  testnet )
    ## Set config params
    ## TODO: Make this more automagic in the future.
    mkdir -p "$(dirname $CONFIG_PATH)"
    echo "rpcuser=lbry" >                       $CONFIG_PATH
    echo "rpcpassword=lbry" >>                  $CONFIG_PATH
    echo "rpcport=29245" >>                     $CONFIG_PATH
    echo "rpcbind=0.0.0.0" >>                   $CONFIG_PATH
    echo "rpcallowip=0.0.0.0/0" >>              $CONFIG_PATH
    echo "testnet=1" >>                         $CONFIG_PATH
    echo "txindex=1" >>                         $CONFIG_PATH
    echo "server=1" >>                          $CONFIG_PATH
    echo "printtoconsole=1" >>                  $CONFIG_PATH
    echo "deprecatedrpc=accounts" >>            $CONFIG_PATH
    echo "deprecatedrpc=validateaddress" >>     $CONFIG_PATH
    echo "deprecatedrpc=signrawtransaction" >>  $CONFIG_PATH

    #nohup advance &>/dev/null &
    lbrycrdd -conf=$CONFIG_PATH $1
    ;;
  * )
    echo "Error, you must define a RUN_MODE environment variable."
    echo "Available options are testnet, regtest, chainquery, default, and reindex"
    ;;
esac