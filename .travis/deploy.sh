#!/bin/bash

docker_repo=$1
docker_tag=git$TRAVIS_COMMIT
helm_config=$2

echo $DOCKER_PASSWORD | docker login --username $DOCKER_USERNAME --password-stdin drbstaging.azurecr.io
docker push $docker_repo:$docker_tag

# Move charts into chart repo
echo Clone charts...
git clone https://github.com/Bankdata/charts.git charts

echo Decrypt kube config
openssl aes-256-cbc -K $encrypted_09f244590a2a_key -iv $encrypted_09f244590a2a_iv -in .travis/kube.config.enc -out kube.config -d

helm upgrade \
  --kubeconfig kube.config \
  -i deployment-cleanup \
  charts/deployment-cleanup \
  --set image.repository=$docker_repo \
  --set image.tag=$docker_tag \
  -f $helm_config
