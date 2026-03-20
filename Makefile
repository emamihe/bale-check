BINARY_NAME := bale-check
INSTALL_DIR := /opt/bale-messenger-countries-check
SERVICE_NAME := bale-countries-check

.PHONY: build run clean install uninstall help

build:
	go build -o $(BINARY_NAME) .

run: build
	sudo ./$(BINARY_NAME)

clean:
	rm -f $(BINARY_NAME)

install: build
	sudo mkdir -p $(INSTALL_DIR)
	sudo cp $(BINARY_NAME) $(INSTALL_DIR)/
	sudo cp config.yaml $(INSTALL_DIR)/
	sudo cp bale-countries-check.service /etc/systemd/system/
	sudo systemctl daemon-reload
	sudo systemctl enable $(SERVICE_NAME)
	sudo systemctl start $(SERVICE_NAME)
	@echo "Installed. Use 'sudo systemctl status $(SERVICE_NAME)' to check."

uninstall:
	sudo systemctl stop $(SERVICE_NAME) 2>/dev/null || true
	sudo systemctl disable $(SERVICE_NAME) 2>/dev/null || true
	sudo rm -f /etc/systemd/system/bale-countries-check.service
	sudo rm -rf $(INSTALL_DIR)
	sudo systemctl daemon-reload
	@echo "Uninstalled."

help:
	@echo "Targets:"
	@echo "  build     - Build the binary"
	@echo "  run       - Build and run (requires sudo for port 443)"
	@echo "  clean     - Remove the binary"
	@echo "  install   - Install to $(INSTALL_DIR) and enable systemd service"
	@echo "  uninstall - Remove installation and disable service"
	@echo "  help      - Show this help"
