variable "prefix" {
  description = "Prefixo dos recursos (curto, minúsculo/alfanum)."
  type        = string
  default     = "k8slab"
}

variable "location" {
  description = "Região Azure (brazilsouth = menor latência p/ BR)."
  type        = string
  default     = "brazilsouth"
}

variable "vm_size" {
  description = "Tamanho da VM. B2s (2 vCPU/4GB) roda k3s+app com folga; B1ms é mais barato mas apertado."
  type        = string
  default     = "Standard_B2s"
}

variable "admin_username" {
  description = "Usuário admin (SSH) da VM."
  type        = string
  default     = "azureuser"
}

variable "ssh_public_key" {
  description = "Sua chave SSH pública (conteúdo do ~/.ssh/id_ed25519.pub)."
  type        = string
}

variable "app_password" {
  description = "Senha compartilhada de acesso (APP_PASSWORD) — o gate para os amigos."
  type        = string
  sensitive   = true
}

variable "allowed_ssh_cidr" {
  description = "CIDR liberado p/ SSH. RECOMENDADO: seu IP (ex.: 200.1.2.3/32). '*' abre pra todos."
  type        = string
  default     = "*"
}

variable "dns_label" {
  description = "Rótulo DNS opcional -> <label>.<região>.cloudapp.azure.com. Vazio = só IP."
  type        = string
  default     = ""
}
