## TODO: Implement a healthcheck for lbrycrd.
curl --data-binary '{"jsonrpc":"1.0","id":"curltext","method":"getinfo","params":[]}' -H 'content-type:text/plain;'  http://$RPC_USER:$RPC_PASSWORD@127.0.0.1:9246
## OR
lbrycrd-cli getinfo