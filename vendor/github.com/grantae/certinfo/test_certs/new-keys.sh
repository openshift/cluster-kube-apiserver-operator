#!/usr/bin/env bash

ROOT1_KEY="root1.key.pem"
LEAF1_KEY="leaf1.key.pem"
LEAF2_KEY="leaf2.key.pem"
LEAF3_KEY="leaf3.key.pem"
LEAF4_KEY="leaf4.key.pem"

# We don't use the keys so make them small
openssl genrsa -out ${ROOT1_KEY} 512
openssl genrsa -out ${LEAF1_KEY} 512
openssl dsaparam -genkey -out ${LEAF2_KEY} -outform PEM 512
openssl ecparam -genkey -out ${LEAF3_KEY} -outform PEM -name secp256k1
openssl ecparam -genkey -out ${LEAF4_KEY} -outform PEM -name secp256k1 -param_enc explicit
