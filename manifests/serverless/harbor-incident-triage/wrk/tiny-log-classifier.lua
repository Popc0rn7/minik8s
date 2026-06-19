local body = '{"data":"{\\"source\\":\\"wrk\\",\\"title\\":\\"NodePort timeout under concurrent load\\",\\"log\\":\\"service endpoint timeout nodeport packet drops iptables security group port unreachable\\",\\"labels\\":{\\"cluster\\":\\"minik8s-demo\\"},\\"demoSleepMs\\":3000}"}'

request = function()
  return "POST /api/v1/namespaces/default/functions/tiny-log-classifier/invoke HTTP/1.1\r\n" ..
         "Host: 127.0.0.1:18080\r\n" ..
         "Content-Type: application/json\r\n" ..
         "Content-Length: " .. string.len(body) .. "\r\n" ..
         "Connection: keep-alive\r\n" ..
         "\r\n" .. body
end
