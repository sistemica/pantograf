<!--
Pantograf example: follow-up email template.

Placeholders use {{NAME}} syntax. The send-templated-email script
fills them with envsubst-style substitution before invoking
`pgf run email/<instance> send-email`.

REQUIRED keys (script will refuse to send if any are unset):
  NAME       — recipient first name
  TOPIC      — short subject (also used as the email subject line)
  BODY       — the LLM-drafted body paragraph(s) — single text block
  SIGNER     — your name
  COMPANY    — your company

The signature block is FIXED — the LLM cannot regenerate it. That's the
"force the template" point: brand & legal lines never change run-to-run.
-->

Hi {{NAME}},

{{BODY}}

Best regards,
{{SIGNER}}
{{COMPANY}}

---
{{COMPANY}}
Sitz der Gesellschaft: Frankfurt am Main
Registergericht: Frankfurt/Main, HRB ...
Geschäftsführer: ...
