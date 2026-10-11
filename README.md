# DayBook

DayBook is a microservice-based personal publishing platform designed for storing, organizing, and publishing various types of records. The system is built around a modern architecture using Java, Go, React, and PostgreSQL.

The backend consists of three independent microservices, while the frontend is a standalone React application communicating with the API Gateway.

---

# Features

* JWT authentication with automatic access token refresh
* User registration and login
* Create, edit, and delete records
* Infinite scrolling record feed
* Live Markdown preview
* Multiple record types
* Parent-child relationships
* Tags
* Visibility control
* Scheduled publication and refresh timestamps
* REST API documented with OpenAPI

---

# Architecture

```text
                        +----------------------+
                        |     React Client     |
                        |        app/          |
                        +----------+-----------+
                                   |
                                   |
                                   v
                     +---------------------------+
                     |       API Gateway         |
                     |          api/             |
                     | Java + Quarkus            |
                     | PostgreSQL Cache          |
                     | Port: 8081                |
                     +------------+--------------+
                                  |
              +-------------------+------------------+
              |                   |                  |
              |                   |                  |
              v                   |                  v
 +----------------------------+   |   +-----------------------------+
 | Authentication Service     |   |   | Core Business Service       |
 | auth/                      |   |   | core/                       |
 | Go                         |   |   | Java + Spring Boot          |
 | Port: 64148                |   |   | Port: 8082                  |
 +----------------------------+   |   +-----------------------------+
              |                   |                  |
              |                   |                  |
              +------+            |             +----+
                     |            |             |
                     |            |             |
                     v            v             v
             +-----------------------------------------+
             |                PostgreSQL               |
             | (persistent cache and application data) |
             +-----------------------------------------+
```

---

# Project Structure

```text
.
├── api/          # API Gateway (Quarkus)
├── auth/         # Authentication Service (Go)
├── core/         # Business Logic Service (Spring Boot)
├── app/          # React + Vite frontend
├── gradle/
└── README.md
```

---

# Components

## app

The frontend application is implemented using:

* React
* TypeScript
* Vite
* React Router
* Axios
* SCSS Modules
* PrimeReact
* React Bootstrap
* Marked

Main responsibilities:

* user authentication
* browsing records
* infinite scrolling
* Markdown preview
* record creation
* record editing
* record deletion

---

## api

API Gateway.

Technology stack:

* Java
* Quarkus
* Hibernate ORM
* RESTEasy Reactive
* PostgreSQL persistent cache

Default port:

```text
8081
```

Responsibilities:

* public REST API
* request routing
* persistent PostgreSQL cache
* aggregation of backend services
* JWT validation
* OpenAPI documentation

Run in development mode:

```bash
cd api
./gradlew quarkusDev
```

---

## auth

Authentication and Authorization Service.

Technology stack:

* Go
* JWT
* Refresh Tokens

Default port:

```text
64148
```

Responsibilities:

* user registration
* login
* logout
* JWT generation
* refresh token management
* access token renewal

Build:

```bash
cd auth
make
```

---

## core

Main business logic service.

Technology stack:

* Java LTS 21+
* Spring Boot
* Spring Data
* PostgreSQL

Default port:

```text
8082
```

Responsibilities:

* record CRUD
* business rules
* tags
* parent-child records
* Markdown records
* JSON records
* XML records
* Set records
* Vector records

Run:

```bash
cd core
./gradlew bootRun
```

(or use the project's preferred Spring Boot startup command.)

---

# Requirements

## Backend

* Java LTS 21+
* Go 1.22+
* Gradle
* GNU Make
* PostgreSQL

## Frontend

* Node.js 20+
* npm

---

# Running the Project

## Root Makefile: host build and test interface

The repository-root `Makefile` is the documented entry point for building and
testing the maintained components on the host. It does **not** yet provide the
local Docker workflow (images, database initialization, `up`/`down`); that is a
later approved increment and is intentionally not implemented here.

```bash
make help                 # list all targets, prerequisites, outputs, gaps
make build                # complete host build: Java -> auth -> app
make build-java           # Gradle wrapper, root: ./gradlew build -x test
make build-auth           # Go 1.26, in auth/: go build + host binary
make build-app            # npm, in app/: npm ci && npm run build
make test-java            # existing Gradle suites (mixed unit/integration)
make test-auth            # Go, in auth/: go test ./...
make test                 # aggregate; always nonzero (frontend tests absent)
make verify-workflow      # inline orchestration assertions (no Docker)
```

Each public operation holds one checkout-wide operation lock under
`.daybook-workflow/` (gitignored) so competing root invocations fail cleanly and
`make -j` runs goals serially. Failed child steps stop dependents and return
nonzero. The host `auth` binary is written to `.daybook-workflow/host/auth-server`
(gitignored) and never overwrites a container binary; `npm run build` outputs to
`app/dist` and never modifies the tracked `app/html` staging.

Prerequisites: GNU Make >= 3.81 and POSIX `/bin/sh` utilities, JDK 21 plus the
Gradle wrapper (Java), Go 1.26 (`auth`), Node/npm compatible with
`app/package-lock.json` (`app`), and Docker for the Java test suites
(Testcontainers).

Capability gaps (honest reporting): frontend tests are not defined in
`app/package.json`, so `make test-app` and aggregate `make test` fail nonzero.
Java suites are existing mixed unit/integration suites, not certified unit-only
coverage measurements. No Docker image/lifecycle targets exist in this
increment.

## Manual service startup

Start the services in the following order.

## 1. Authentication Service

```bash
cd auth
make build start
```

Runs on:

```text
http://localhost:64148
```

---

## 2. Core Service

```bash
cd core
./gradlew bootRun
```

Runs on:

```text
http://localhost:8082
```

---

## 3. API Gateway

```bash
cd api
./gradlew quarkusDev
```

Runs on:

```text
http://localhost:8081
```

---

## 4. Frontend

```bash
cd app
npm install
npm run dev
```

The frontend communicates exclusively with the API Gateway.

---

# Record Types

The platform currently supports:

* Markdown
* JSON
* XML
* Set
* Vector

Markdown records include a live HTML preview while editing.

---

# Authentication Flow

1. User authenticates through the Authentication Service.
2. JWT access token is issued.
3. Refresh token is stored as an HTTP-only cookie.
4. The frontend automatically renews expired access tokens.
5. API Gateway validates JWT tokens before forwarding requests.

---

# REST API

The API Gateway exposes REST endpoints for:

* authentication
* users
* records
* search
* tags
* CRUD operations

The OpenAPI specification is available through the API Gateway while running in development mode.

---

# Technology Stack

## Frontend

* React
* TypeScript
* Vite
* React Router
* Axios
* SCSS Modules
* PrimeReact
* React Bootstrap
* Marked

## API Gateway

* Java
* Quarkus
* Hibernate ORM
* RESTEasy Reactive

## Authentication Service

* Go
* JWT

## Core Service

* Java
* Spring Boot
* Spring Data

## Database

* PostgreSQL

---

# Development Status

Implemented features include:

* JWT authentication
* automatic access token refresh
* API Gateway
* persistent PostgreSQL cache
* microservice architecture
* infinite scrolling
* record CRUD
* Markdown live preview
* record editing
* record deletion
* multiple record types
* protected routes

The project is actively under development, and additional features will continue to be added.

---

# License

This project is distributed under the license selected by the repository owner.
