<p align="center">
<img src="https://raw.githubusercontent.com/Aladex/gopds-api/master/logo/logo.png" width="350">
</p>

# gopds-api

Welcome to the gopds-api project! This project is an implementation of the SOPDS database, and provides an API for interacting with it.

## Technologies

The gopds-api is implemented using Go, and utilizes Redis for session store. The documentation for the API is generated automatically using Swaggo swagger.

## Features

The gopds-api has several features, including:
- A list of scanned books
- The ability to search for users and authors in the database
- The ability to download raw FB2 books from .zip archives
- Login and logout functionality similar to Django
- Authentication of all requests using JWT tokens and the session store
- Adding books to a user's favorite list
- An OPDS server with authentication to allow users to access their favorite books from different devices

## Roadmap

In the future, we have plans to add:
- A book scanner for FB2 and EPUB formats
- A converter that can transform FB2 files into EPUB and MOBI formats
- A Telegram bot to allow users to access their favorite books and perform actions such as adding books to their favorite list through the messaging platform.

## Bindata create

To create bindata, you can use the following command:
```
go-bindata -o static_assets/bindata.go -fs -prefix "posters" -pkg static_assets static_assets/posters/...
```