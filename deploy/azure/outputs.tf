output "acr_name" {
  description = "Nome da ACR — use no 'az acr build' para publicar a imagem."
  value       = azurerm_container_registry.lab.name
}

output "public_ip" {
  description = "IP público da instância."
  value       = azurerm_public_ip.lab.ip_address
}

output "app_url" {
  description = "URL (HTTPS) para os amigos acessarem no browser."
  value       = "https://${azurerm_public_ip.lab.fqdn}"
}

output "ssh" {
  description = "Comando SSH para administrar a VM."
  value       = "ssh ${var.admin_username}@${azurerm_public_ip.lab.ip_address}"
}

output "resource_group" {
  description = "Resource group (p/ az vm start/stop)."
  value       = azurerm_resource_group.lab.name
}

output "vm_name" {
  description = "Nome da VM (p/ az vm start/stop)."
  value       = azurerm_linux_virtual_machine.lab.name
}

output "postgres_fqdn" {
  description = "FQDN privado do PostgreSQL Flexible Server."
  value       = azurerm_postgresql_flexible_server.lab.fqdn
}

output "key_vault_name" {
  description = "Key Vault que guarda a URL do banco para a managed identity da VM."
  value       = azurerm_key_vault.lab.name
}
