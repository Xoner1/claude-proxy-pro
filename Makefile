.PHONY: dev build build-mac build-win build-linux clean

dev:
	wails dev

build:
	wails build

build-mac:
	wails build -platform darwin/universal

build-win:
	wails build -platform windows/amd64

build-linux:
	wails build -platform linux/amd64

clean:
	rm -rf build/bin
	rm -rf frontend/dist

release:
	@echo "Building for all platforms..."
	wails build -platform darwin/universal,windows/amd64,linux/amd64
