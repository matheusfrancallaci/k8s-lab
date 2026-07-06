# ─────────────────────────────────────────────────────────────────────────────
# K8s Study Lab — deploy de UMA instância hospedada na Azure.
# VM Linux barata rodando o container completo (app + k3s embutido), acessível
# pelo browser dos amigos. Imagem construída na ACR (az acr build) e puxada pela
# VM via managed identity. ACI/Container Apps não servem (não permitem privileged).
# ─────────────────────────────────────────────────────────────────────────────

terraform {
  required_version = ">= 1.5"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}

provider "azurerm" {
  features {}
}

# Sufixo p/ nomes globalmente únicos (ACR).
resource "random_string" "suffix" {
  length  = 5
  special = false
  upper   = false
}

resource "azurerm_resource_group" "lab" {
  name     = "${var.prefix}-rg"
  location = var.location
}

# ── Registry: a imagem é construída aqui (az acr build) e a VM puxa daqui ──
resource "azurerm_container_registry" "lab" {
  name                = replace("${var.prefix}acr${random_string.suffix.result}", "-", "")
  resource_group_name = azurerm_resource_group.lab.name
  location            = azurerm_resource_group.lab.location
  sku                 = "Basic"
  admin_enabled       = false
}

# ── Rede ──
resource "azurerm_virtual_network" "lab" {
  name                = "${var.prefix}-vnet"
  resource_group_name = azurerm_resource_group.lab.name
  location            = azurerm_resource_group.lab.location
  address_space       = ["10.20.0.0/16"]
}

resource "azurerm_subnet" "lab" {
  name                 = "${var.prefix}-subnet"
  resource_group_name  = azurerm_resource_group.lab.name
  virtual_network_name = azurerm_virtual_network.lab.name
  address_prefixes     = ["10.20.1.0/24"]
}

resource "azurerm_public_ip" "lab" {
  name                = "${var.prefix}-ip"
  resource_group_name = azurerm_resource_group.lab.name
  location            = azurerm_resource_group.lab.location
  allocation_method   = "Static"
  sku                 = "Standard"
  # Sempre gera um FQDN (<label>.<região>.cloudapp.azure.com) — o Caddy usa ele
  # para emitir HTTPS automático via Let's Encrypt.
  domain_name_label = var.dns_label != "" ? var.dns_label : "${var.prefix}-${random_string.suffix.result}"
}

resource "azurerm_network_security_group" "lab" {
  name                = "${var.prefix}-nsg"
  resource_group_name = azurerm_resource_group.lab.name
  location            = azurerm_resource_group.lab.location

  # SSH — restrinja ao SEU IP em var.allowed_ssh_cidr
  security_rule {
    name                       = "ssh"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "22"
    source_address_prefix      = var.allowed_ssh_cidr
    destination_address_prefix = "*"
  }

  # HTTP — redireciona p/ HTTPS (e valida o cert Let's Encrypt via :80)
  security_rule {
    name                       = "http"
    priority                   = 110
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "80"
    source_address_prefix      = "*"
    destination_address_prefix = "*"
  }

  # HTTPS — os amigos acessam por aqui
  security_rule {
    name                       = "https"
    priority                   = 120
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "443"
    source_address_prefix      = "*"
    destination_address_prefix = "*"
  }
}

resource "azurerm_network_interface" "lab" {
  name                = "${var.prefix}-nic"
  resource_group_name = azurerm_resource_group.lab.name
  location            = azurerm_resource_group.lab.location

  ip_configuration {
    name                          = "internal"
    subnet_id                     = azurerm_subnet.lab.id
    private_ip_address_allocation = "Dynamic"
    public_ip_address_id          = azurerm_public_ip.lab.id
  }
}

resource "azurerm_network_interface_security_group_association" "lab" {
  network_interface_id      = azurerm_network_interface.lab.id
  network_security_group_id = azurerm_network_security_group.lab.id
}

# ── VM ──
resource "azurerm_linux_virtual_machine" "lab" {
  name                = "${var.prefix}-vm"
  resource_group_name = azurerm_resource_group.lab.name
  location            = azurerm_resource_group.lab.location
  size                = var.vm_size
  admin_username      = var.admin_username

  network_interface_ids = [azurerm_network_interface.lab.id]

  identity {
    type = "SystemAssigned"
  }

  admin_ssh_key {
    username   = var.admin_username
    public_key = var.ssh_public_key
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "StandardSSD_LRS"
    disk_size_gb         = 32
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "0001-com-ubuntu-server-jammy"
    sku       = "22_04-lts-gen2"
    version   = "latest"
  }

  custom_data = base64encode(templatefile("${path.module}/cloud-init.yaml", {
    acr_name     = azurerm_container_registry.lab.name
    image        = "${azurerm_container_registry.lab.login_server}/estudo-app:latest"
    app_password = var.app_password
    fqdn         = azurerm_public_ip.lab.fqdn
  }))
}

# ── A VM puxa a imagem da ACR com a própria identity (sem senha) ──
resource "azurerm_role_assignment" "acr_pull" {
  scope                = azurerm_container_registry.lab.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_linux_virtual_machine.lab.identity[0].principal_id
}
