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
