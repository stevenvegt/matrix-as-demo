# Matrix.org App Service demo

Based on mautrix.
The goal is to learn the basic concepts of an application service for matrix.org.

## Usage

Create an .env file with the following content:

```
AS_TOKEN=<application service token>
HS_TOKEN=<homeserver token>
HOMESERVER_HOST=<homeserver host>
USER_ID=@<user>:<homeserver host>
```

You can choose the tokens yourself.

The user id should be an existing user on the homeserver.

## Run

```
go run main.go
```

## Usage

The application service starts a server on `localhost:1237`

When you open `localhost:1237` in your browser, you should see a list with messages and an input field were you can type.

You can only chat with unencrypted rooms.
