#!/bin/sh

up_auth() {
  docker-compose up -d golang-auth || exit 2
}

up_api() {
  docker-compose up -d quarkus-3-33-api || exit 2
}

up_core() {
  docker-compose up -d spring-3-5-core || exit 2
}

up_app() {
  docker-compose up -d nginx-react-app || exit 2
}

up_postgres() {
  docker-compose up -d postgres-db || exit 2
}

cd ./docker/ || exit 1
case $1 in
api)
  up_api
  ;;
app)
  up_app
  ;;
auth)
  up_auth
  ;;
core)
  up_core
  ;;
*)
  up_postgres
  up_auth
  up_api
  up_core
  up_app
  ;;
esac
cd - || exit 1