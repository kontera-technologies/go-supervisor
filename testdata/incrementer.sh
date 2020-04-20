#!/usr/bin/env ruby

require 'json'
STDOUT.sync = true

STDIN.each_line do |l|
  begin
    l = JSON(l.chomp)
    puts({greetings: "from incrementer", msg: l["msg"], prev: l["num"], num: l["num"]+1}.to_json)
  rescue StandardError => e
    STDERR.puts e
  end
end