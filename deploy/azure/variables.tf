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
  description = "Tamanho da VM. D4s_v3 (4 vCPU/16GB) suporta k3s, app e Qwen3 8B com margem."
  type        = string
  default     = "Standard_D4s_v3"
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

variable "idle_minutes" {
  description = "Minutos de inatividade (sem terminais ativos) antes da VM se desalocar sozinha. 0 desliga o auto-stop."
  type        = number
  default     = 30
}

variable "ollama_model" {
  description = "Modelo local de chat. qwen3:8b prioriza qualidade grounded na VM de 16GB. Vazio desliga a IA."
  type        = string
  default     = "qwen3:8b"
}

variable "ollama_router_model" {
  description = "Modelo menor para classificacao e planejamento curto; reduz a latencia sem rebaixar o gerador de labs."
  type        = string
  default     = "qwen3:4b"
}

variable "ollama_gen_model" {
  description = "Modelo dedicado a geracao estruturada de labs e quizzes. Mantido separado do chat para priorizar codigo."
  type        = string
  default     = "qwen3:8b"
}

variable "ollama_embed_model" {
  description = "Modelo Ollama dedicado a embeddings persistidos do RAG. Vazio usa fallback local sem embedding neural."
  type        = string
  default     = "embeddinggemma"
}
