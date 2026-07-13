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
      source = "hashicorp/azurerm"
      # 3.x maduro: a linha 4.x recente estava dando "inconsistent result after
      # apply" em recursos de rede (vnet/nsg/ip) — bug de leitura pos-criacao.
      version = ">= 3.110, < 4.0"
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

resource "azurerm_subnet" "postgres" {
  name                 = "${var.prefix}-postgres-subnet"
  resource_group_name  = azurerm_resource_group.lab.name
  virtual_network_name = azurerm_virtual_network.lab.name
  address_prefixes     = ["10.20.2.0/24"]

  delegation {
    name = "postgres-flexible-server"
    service_delegation {
      name    = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

resource "azurerm_private_dns_zone" "postgres" {
  name                = "${var.prefix}.postgres.database.azure.com"
  resource_group_name = azurerm_resource_group.lab.name
}

resource "azurerm_private_dns_zone_virtual_network_link" "postgres" {
  name                  = "${var.prefix}-postgres-link"
  private_dns_zone_name = azurerm_private_dns_zone.postgres.name
  virtual_network_id    = azurerm_virtual_network.lab.id
  resource_group_name   = azurerm_resource_group.lab.name
}

resource "random_password" "postgres" {
  length  = 32
  special = false
}

resource "azurerm_postgresql_flexible_server" "lab" {
  name                          = "${var.prefix}-postgres-${random_string.suffix.result}"
  resource_group_name           = azurerm_resource_group.lab.name
  location                      = azurerm_resource_group.lab.location
  version                       = var.postgres_version
  delegated_subnet_id           = azurerm_subnet.postgres.id
  private_dns_zone_id           = azurerm_private_dns_zone.postgres.id
  public_network_access_enabled = false
  administrator_login           = "labadmin"
  administrator_password        = random_password.postgres.result
  zone                          = "1"
  storage_mb                    = var.postgres_storage_mb
  sku_name                      = var.postgres_sku_name
  backup_retention_days         = 7

  depends_on = [azurerm_private_dns_zone_virtual_network_link.postgres]
}

resource "azurerm_postgresql_flexible_server_database" "lab" {
  name      = "estudo_app"
  server_id = azurerm_postgresql_flexible_server.lab.id
  charset   = "UTF8"
  collation = "en_US.utf8"
}

data "azurerm_client_config" "current" {}

resource "azurerm_key_vault" "lab" {
  name                       = "${var.prefix}kv${random_string.suffix.result}"
  resource_group_name        = azurerm_resource_group.lab.name
  location                   = azurerm_resource_group.lab.location
  tenant_id                  = data.azurerm_client_config.current.tenant_id
  sku_name                   = "standard"
  enable_rbac_authorization  = true
  soft_delete_retention_days = 7
}

resource "azurerm_role_assignment" "deployer_manage_database_secret" {
  scope                = azurerm_key_vault.lab.id
  role_definition_name = "Key Vault Secrets Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}

resource "azurerm_key_vault_secret" "database_url" {
  name         = "database-url"
  value        = "postgres://labadmin:${random_password.postgres.result}@${azurerm_postgresql_flexible_server.lab.fqdn}:5432/${azurerm_postgresql_flexible_server_database.lab.name}?sslmode=require"
  key_vault_id = azurerm_key_vault.lab.id
  depends_on   = [azurerm_role_assignment.deployer_manage_database_secret]
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
    acr_name               = azurerm_container_registry.lab.name
    image                  = "${azurerm_container_registry.lab.login_server}/estudo-app:latest"
    app_password           = var.app_password
    fqdn                   = azurerm_public_ip.lab.fqdn
    rg_name                = azurerm_resource_group.lab.name
    location               = azurerm_resource_group.lab.location
    vm_name                = "${var.prefix}-vm"
    idle_threshold_seconds = var.idle_minutes * 60
    ollama_model           = var.ollama_model
    ollama_router_model    = var.ollama_router_model
    ollama_gen_model       = var.ollama_gen_model
    ollama_embed_model     = var.ollama_embed_model
  }))
}

# ── A VM puxa a imagem da ACR com a própria identity (sem senha) ──
resource "azurerm_role_assignment" "acr_pull" {
  scope                = azurerm_container_registry.lab.id
  role_definition_name = "AcrPull"
  principal_id         = azurerm_linux_virtual_machine.lab.identity[0].principal_id
}

# ── A VM pode se desalocar sozinha (auto-stop por inatividade) ──
resource "azurerm_role_assignment" "vm_self_manage" {
  scope                = azurerm_linux_virtual_machine.lab.id
  role_definition_name = "Virtual Machine Contributor"
  principal_id         = azurerm_linux_virtual_machine.lab.identity[0].principal_id
}

# ── A identity da VM gerencia AKS no RG (login por identity, sem device-code) ──
# Necessário para a página Cloud criar/ligar/conectar o AKS automaticamente na
# instância hospedada. Escopo = só este resource group (não a subscription toda).
resource "azurerm_role_assignment" "vm_manage_rg" {
  scope                = azurerm_resource_group.lab.id
  role_definition_name = "Contributor"
  principal_id         = azurerm_linux_virtual_machine.lab.identity[0].principal_id
}

resource "azurerm_role_assignment" "vm_read_database_secret" {
  scope                = azurerm_key_vault.lab.id
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_linux_virtual_machine.lab.identity[0].principal_id
}
