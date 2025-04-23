# Tiramisu

Generates a flow diagram for each data cube (only 1-level deep, incoming/outgoing flows). It requires a `data.json` file in the root of the directory containing the results of the Solution Builder request.

Diagrams are generated using [D2](https://d2lang.com/).

## Requirements
- Go

## Run

```sh
go run src/main.go
```