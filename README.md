# Glow Baby data exporter

This is a program that dowloads the data from [Glow
Baby](https://baby.glowing.com/) into an
[SQLite3](https://www.sqlite.org/index.html) database file.

## Usage

You'll need [Go](https://golang.org/) installed and set up.

Copy the included `glowbabyrc` file into `$HOME/.glowbabyrc`, update it to use
your own email and password, then:

  1. `go build` (builds the `glowbaby` tool)
  2. `./glowbaby init` (this prepares the `baby.db` file)
  3. `./glowbaby login` (logs in to baby.glowing.com and identifies your babies)
  4. `./glowbaby sync` (refresh the local data)

Repeat the final step as needed.
