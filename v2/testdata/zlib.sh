#!/usr/bin/env ruby

require 'zlib'
require 'json'
require 'base64'

STDOUT.sync = true

STDIN.each_line do |l|
  begin
    buf = Zlib::Inflate.inflate Base64::strict_decode64 l.chomp
    puts Base64::strict_encode64 Zlib::Deflate.deflate({hello: "from producer", msg: buf, num: rand(1000)}.to_json + "\n")
  rescue StandardError => e
    STDERR.puts "#{e.to_s}: #{e.backtrace[0].to_s}"
  end
end
