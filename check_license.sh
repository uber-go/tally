#!/bin/bash

uber-licence --version || npm i uber-licence@latest -g
uber-licence --dry --file "*.go"
