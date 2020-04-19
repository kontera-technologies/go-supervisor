#!/usr/bin/env ruby

require 'json'
STDOUT.sync = true

STDIN.each_line do |l|
  begin
    puts({hello: "from producer", msg: l.chomp, num: rand(1000)}.to_json)
  rescue StandardError => e
    STDERR.puts e
  end
end