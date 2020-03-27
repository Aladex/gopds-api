# gopds-api
Implementation of SOPDS project

This repository contains an implementation for SOPDS database from [SOPDS mitshel project](https://github.com/mitshel/sopds) 

It works with database and can to authorize users from SOPDS typical django database with pbkdf2.
 
Documentation is realized with swaggo swagger and generates automatically.

## Technologies

* Redis (for sessions store)
* PostgreSQL (An django database, that generated from SOPDS project)
* Go libs from go.mod


## Features

1. List of scanned books
2. Search for users and authors in database
3. Download of raw fb2 book from .zip archive
4. login and logout methods like in django
5. Authentication of all requests by JWT token and session store


## Roadmap

1. Projects database with own structure
2. Book scanner for fb2 and epub formats
3. Converter from fb2 to epub, mobi

