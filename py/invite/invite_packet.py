import time
import struct
import random
import binascii

from socket import AF_INET, AF_INET6
try:
    from socket import inet_pton, inet_ntop
except ImportError:
    from invite.win32_inet_pton import inet_pton, inet_ntop

from skein import skein512

from core.ec_packet import DataPacket
from core.util import getPublicIP, splitFields, encodeFlags, decodeFlags, fill
from crypto.curve import Key

PUBKEY_LENGTH=32
FLAGS_LENGTH=1
IP_LENGTH=16
PORT_LENGTH=2
ID_LENGTH=16
SECRET_LENGTH=16

IPV4_LENGTH=4

class InviteMessage:
  def __init__(self):
    self.pubkey=None
    self.v6=None
    self.tcp=None
    self.ip=None
    self.port=None
    self.address=None
    self.id=None
    self.secret=None
    
  def __str__(self):
    return "<InviteMessage(%s,%s,%s,%s,%s,%s,%s)" % (self.pubkey, self.v6, self.tcp, self.ip, self.port, self.id, self.secret)

  def generate(self, pubkey, v6, tcp, port):
    self.pubkey=pubkey
    self.v6=v6
    self.tcp=tcp
    self.ip=getPublicIP(v6)
    self.port=port
    self.id=self.makeIdentifier()
    self.secret = self.makeSecret()
        
    pubkey=self.pubkey.bytes
    flags=encodeFlags((self.v6, self.tcp, False, False, False, False, False, False))
    if self.v6:
      ip=inet_pton(AF_INET6, self.ip)
    else:
      print('v4:', self.ip)
      ip=inet_pton(AF_INET, self.ip)
      print('ip:', ip)
      ip=fill(ip, IP_LENGTH)
    port=struct.pack('H', self.port)
    id=self.id
    secret=self.secret

    self.message=pubkey+flags+ip+port+id+secret
    
  def makeIdentifier(self):
    return bytes(random.randint(0, 255) for _ in range(16))
      
  def makeSecret(self):
    return bytes(random.randint(0, 255) for _ in range(16))
    
  def createInviteMessage(self, pubkey, v6, tcp, ip, port, id, secret):
    self.pubkey=pubkey
    self.v6=v6
    self.tcp=tcp
    self.ip=ip
    self.port=port
    self.id=id
    self.secret=secret
    
    pubkey=self.pubkey.bytes
    flags=encodeFlags((self.v6, self.tcp, False, False, False, False, False, False))
    if self.v6:
      ip=inet_pton(AF_INET6, self.ip)
    else:
      ip=inet_pton(AF_INET, self.ip)
      ip=fill(ip, IP_LENGTH)
    port=struct.pack('H', self.port)
    id=self.id
    secret=self.secret
    
    self.message=pubkey+flags+ip+port+id+secret
    
  def decodeInviteMessage(self, message):
    self.message=message
    
    pubkey, flags, ip, port, id, secret=splitFields(self.message, [PUBKEY_LENGTH, FLAGS_LENGTH, IP_LENGTH, PORT_LENGTH, ID_LENGTH, SECRET_LENGTH])
    self.pubkey=Key(pubkey, False)
    flags=decodeFlags(flags)
    self.v6=flags[0]
    self.tcp=flags[1]
    if self.v6:
      self.ip=inet_ntop(AF_INET6, ip)
    else:
      ip=ip[:IPV4_LENGTH]
      self.ip=inet_ntop(AF_INET, ip)
    self.port=struct.unpack("H", port)[0]
    self.id=id
    self.secret=secret

class InvitePacket(DataPacket):
  def __init__(self):
    DataPacket.__init__(self)

    self.invite=None
    
  def createInvitePacket(self, password, invite):
    self.invite=invite
    
    sk=skein512(password.encode('ascii'), digest_bits=256).digest()
    self.createDataPacket(sk, self.invite.message)
  
  def decodeInvitePacket(self, password, packet):
    sk=skein512(password.encode('ascii'), digest_bits=256).digest()
    
    self.decodeDataPacket(sk, packet)
    self.invite=InviteMessage()
    self.invite.decodeInviteMessage(self.data)
    return self.invite
      