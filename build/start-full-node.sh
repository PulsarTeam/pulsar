#!/bin/bash

bin/geth -rpc -rpcapi 'web3,eth,debug,personal' -rpcport 8545 --rpccorsdomain '*' --datadir ~/data/ --networkid 15 --verbosity 6 --nodiscover console
#bin/geth -rpc -rpcapi 'web3,eth,debug,personal' -rpcport 8545 --rpccorsdomain '*' --datadir ~/data/ --networkid 15 --verbosity 6 console
