param([string]$NetworkScript,[string]$StateRoot)
$ErrorActionPreference='Stop'
# These functions shadow every networking cmdlet used by multi.ps1. This test
# must never access or modify the real routing table, interfaces, or NRPT.
$global:routes=@([pscustomobject]@{DestinationPrefix='0.0.0.0/0';InterfaceIndex=1;InterfaceAlias='Ethernet';NextHop='192.0.2.1';RouteMetric=0})
$global:rules=@()
$global:ips=@()
$global:failRoute=''
function global:Get-NetRoute { param($DestinationPrefix,$InterfaceIndex,$AddressFamily,$PolicyStore,$ErrorAction)
    @($global:routes | Where-Object { (!$DestinationPrefix -or $_.DestinationPrefix -eq $DestinationPrefix) -and (!$InterfaceIndex -or $_.InterfaceIndex -eq $InterfaceIndex) })
}
function global:New-NetRoute { param($DestinationPrefix,$InterfaceIndex,$NextHop,$RouteMetric,$PolicyStore)
    if ($DestinationPrefix -eq $global:failRoute) { throw 'simulated route failure' }
    $global:routes += [pscustomobject]@{DestinationPrefix=$DestinationPrefix;InterfaceIndex=$InterfaceIndex;NextHop=$NextHop;RouteMetric=$RouteMetric;InterfaceAlias=$env:OCM_INTERFACE}
}
function global:Remove-NetRoute { param([Parameter(ValueFromPipeline)]$InputObject,[switch]$Confirm)
    process { $global:routes=@($global:routes | Where-Object { $_ -ne $InputObject }) }
}
function global:Get-NetIPInterface { param($InterfaceIndex,$AddressFamily) [pscustomobject]@{InterfaceMetric=10} }
function global:Set-NetIPInterface { param($InterfaceIndex,$AddressFamily,$Dhcp,$AutomaticMetric,$InterfaceMetric,$NlMtuBytes) }
function global:Get-NetAdapter { param($Name,[switch]$IncludeHidden,$ErrorAction)
    if (!$Name -or $Name -eq $env:OCM_INTERFACE) { [pscustomobject]@{Name=$env:OCM_INTERFACE;ifIndex=[int]$env:TUNIDX} }
}
function global:New-NetIPAddress { param($InterfaceIndex,$IPAddress,$PrefixLength,$AddressFamily,$PolicyStore)
    $global:ips += [pscustomobject]@{InterfaceIndex=$InterfaceIndex;IPAddress=$IPAddress}
}
function global:Get-NetIPAddress { param($InterfaceIndex,$AddressFamily,$ErrorAction) @($global:ips | Where-Object InterfaceIndex -eq $InterfaceIndex) }
function global:Remove-NetIPAddress { param([Parameter(ValueFromPipeline)]$InputObject,[switch]$Confirm)
    process { $global:ips=@($global:ips | Where-Object { $_ -ne $InputObject }) }
}
function global:Get-DnsClientNrptRule { @($global:rules) }
function global:Add-DnsClientNrptRule { param($Namespace,$NameServers,$Comment,[switch]$PassThru)
    $rule=[pscustomobject]@{Name=[guid]::NewGuid().ToString();Namespace=$Namespace;NameServers=$NameServers;Comment=$Comment}
    $global:rules += $rule
    if ($PassThru) { $rule }
}
function global:Remove-DnsClientNrptRule { param($Name,[switch]$Force)
    $global:rules=@($global:rules | Where-Object Name -ne $Name)
}
function Session([string]$ID,[int]$Index,[string]$Address,[string]$Network,[string]$Domain,[bool]$Full=$false) {
    $env:OCM_NETWORK_ROOT=$StateRoot
    $env:OCM_STATE_FILE=Join-Path $StateRoot ($ID+'.json')
    $env:OCM_INTERFACE='OCM-'+$ID
    $env:OCM_ROUTES='[]';$env:OCM_DNS_DOMAINS='[]';$env:OCM_FULL=$Full.ToString().ToLowerInvariant()
    $env:TUNIDX=[string]$Index;$env:INTERNAL_IP4_ADDRESS=$Address;$env:INTERNAL_IP4_MTU=''
    $env:VPNGATEWAY='198.51.100.5';$env:CISCO_SPLIT_INC='1';$env:CISCO_SPLIT_INC_0_ADDR=$Network;$env:CISCO_SPLIT_INC_0_MASKLEN='24'
    $env:INTERNAL_IP4_DNS=$Network -replace '\.0$','.53';$env:CISCO_DEF_DOMAIN=$Domain;$env:CISCO_SPLIT_DNS='';$env:CISCO_SPLIT_EXC='0'
}
function Apply { $env:reason='connect'; & $NetworkScript; if ($LASTEXITCODE -ne 0) { $global:LASTEXITCODE=0 } }
function Stop-Session { $env:reason='disconnect'; & $NetworkScript }
function Assert([bool]$Condition,[string]$Message) { if (!$Condition) { throw $Message } }

Session '1111111111111111' 11 '10.70.0.2' '10.11.0.0' 'internet.test' $true
Apply
Assert ((Get-Content -Raw $env:OCM_STATE_FILE | ConvertFrom-Json).ready) 'Internet tunnel not ready'
Assert (@($global:routes | Where-Object DestinationPrefix -eq '0.0.0.0/1').Count -eq 1) 'Missing Internet default pair'
Session '2222222222222222' 12 '10.71.0.2' '10.12.0.0' 'work.test'
Apply
Assert ((Get-Content -Raw $env:OCM_STATE_FILE | ConvertFrom-Json).ready) 'Work tunnel not ready'
Assert ($global:rules.Count -eq 3) 'Split DNS and default DNS missing'
Session '1111111111111111' 11 '10.70.0.2' '10.11.0.0' 'internet.test' $true
Stop-Session
Assert (@($global:routes | Where-Object InterfaceIndex -eq 12).Count -gt 0) 'Stopping Internet tunnel removed work routes'
Assert (@($global:routes | Where-Object DestinationPrefix -eq '198.51.100.5/32').Count -eq 1) 'Shared endpoint lost when its original owner stopped'
Apply
Session '2222222222222222' 12 '10.71.0.2' '10.12.0.0' 'work.test'
Stop-Session
Assert (@($global:routes | Where-Object InterfaceIndex -eq 11).Count -gt 0) 'Stopping work tunnel removed Internet routes'
Assert (@($global:rules | Where-Object Comment -eq 'OCM-1111111111111111').Count -eq 2) 'Stopping work tunnel removed Internet DNS'
Assert (@($global:routes | Where-Object DestinationPrefix -eq '198.51.100.5/32').Count -eq 1) 'Shared endpoint route was removed too soon'

Session '3333333333333333' 13 '10.72.0.2' '10.13.0.0' 'failure.test'
$global:failRoute='10.13.0.0/24'
try { Apply } catch { }
$failed=Get-Content -Raw $env:OCM_STATE_FILE | ConvertFrom-Json
Assert (!$failed.ready -and $failed.error -like '*simulated route failure*') 'Failure not surfaced'
Assert (@($global:routes | Where-Object InterfaceIndex -eq 13).Count -eq 0) 'Partial route setup was not rolled back'
Assert (@($global:routes | Where-Object InterfaceIndex -eq 11).Count -gt 0) 'Rollback disturbed another tunnel'
$global:failRoute=''

Session '4444444444444444' 14 '10.73.0.2' '10.11.0.0' 'conflict.test'
try { Apply } catch { }
$failed=Get-Content -Raw $env:OCM_STATE_FILE | ConvertFrom-Json
Assert ($failed.error -like '*Work network conflict*') 'Overlapping routes were accepted'
Assert (@($global:routes | Where-Object InterfaceIndex -eq 11).Count -gt 0) 'Conflict rollback removed working routes'

Session '1111111111111111' 11 '10.70.0.2' '10.11.0.0' 'internet.test' $true
Stop-Session
Assert ($global:routes.Count -eq 1 -and $global:routes[0].DestinationPrefix -eq '0.0.0.0/0') 'Owned routes left behind or original route removed'
Assert ($global:rules.Count -eq 0) 'DNS rules left behind'
Assert ($global:ips.Count -eq 0) 'Tunnel addresses left behind'
Write-Output 'Mock network lifecycle passed'
