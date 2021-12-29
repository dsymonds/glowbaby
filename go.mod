module github.com/dsymonds/glowbaby

go 1.17

require github.com/mattn/go-sqlite3 v1.14.9

require (
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	golang.org/x/image v0.0.0-20211028202545-6944b10bf410 // indirect
)

replace github.com/mattn/go-sqlite3 => github.com/dsymonds/go-sqlite3 v1.14.10-0.20211216223514-f541e6d23bc6
