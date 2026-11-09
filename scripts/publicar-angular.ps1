param(
    [Parameter(Mandatory = $true)]
    [string]$Origen
)

$ErrorActionPreference = 'Stop'
$destino = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\ClientApp'))
$origenResuelto = [IO.Path]::GetFullPath($Origen)
$raizApi = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

if (-not $destino.StartsWith($raizApi, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'La carpeta ClientApp resuelta queda fuera de crm-api.'
}
if (-not (Test-Path -LiteralPath (Join-Path $origenResuelto 'index.html'))) {
    throw "El origen no contiene index.html: $origenResuelto"
}

Get-ChildItem -LiteralPath $destino -Force |
    Where-Object { $_.Name -ne 'README.md' } |
    Remove-Item -Recurse -Force

Get-ChildItem -LiteralPath $origenResuelto -Force |
    Copy-Item -Destination $destino -Recurse -Force
Write-Host "Angular publicado en $destino"
