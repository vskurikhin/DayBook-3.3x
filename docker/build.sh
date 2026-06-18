#!/bin/sh

build_auth() {
  cd ./auth || exit 1
  GOOS=linux GOARCH=arm64 make clean build || exit 2
  docker build -f Dockerfile -t svn/auth .
  make clean build
  cd - || exit 1
}

build_api() {
  ./gradlew :api:build -xtest \
      -Dquarkus.package.jar.enabled=false \
      -Dquarkus.native.enabled=true \
      -Dquarkus.native.container-build=true \
      -Dquarkus.container-image.build=true ||
      exit 2

  ./gradlew properties -q | grep "^version:" | awk '{print $2}' |
  while IFS='' read -r VERSION
  do
    echo "VERSION:$VERSION"
    docker tag svn/api:"$VERSION" svn/api:latest
  done
}

build_core() {
  ./gradlew :core:build -xtest || exit 2

  cd core || exit 1
  docker build -f ./src/main/resources/Dockerfile -t svn/core .
  cd - || exit 1
}

build_app() {
  cd ./app || exit 1
  npm run build || exit 2

  cp -f ./dist/index.html ./html/
  cp -f ./dist/index.html ../docker/nginx/html/
  rm -f ./html/assets/*
  cp -f ./dist/assets/* ./html/assets/
  rm -f ../docker/nginx/html/assets/*
  cp -f ./dist/assets/* ../docker/nginx/html/assets/

  cd - || exit 1

  cd ./docker/nginx || exit 1
  docker build -f Dockerfile -t svn/app .
  cd - || exit 1
}

case $1 in
api)
  build_api
  ;;
app)
  build_app
  ;;
auth)
  build_auth
  ;;
core)
  build_core
  ;;
*)
  build_auth
  build_api
  build_core
  build_app
  ;;
esac