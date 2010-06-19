#!/usr/bin/python3.1
import os
import sys
import time
import glob

from dust.crypto.curve import Key
from dust.crypto.keys import KeyManager
from dust.extensions.onion.onion_packet import OnionPacket
from dust.util.ymap import YamlMap
from dust.core.util import getPublicIP, encode, decode, encodeAddress, decodeAddress
from dust.invite.invite_packet import InviteMessage
from dust.services.dustmail.dustmail_packet import DustmailInvitePacket

from threading import Event
from dust.server.router import PacketRouter
from dust.util.safethread import waitForEvent
from dust.extensions.onion.onion_packet import OnionPacket
from dust.services.tracker.trackerClient import TrackerClient
from dust.services.dustmail.dustmailClient import DustmailClient

class DustmailReader:
  def __init__(self, router, endpoint):
    self.router=router
    self.endpoint=endpoint

    self.keys=router.keys
    self.maildir='spool/'+encode(endpoint.public.bytes)
    self.addressBook=YamlMap('config/dustmail-addressbook.yaml')
    self.done=Event()
    self.commandDone=Event()

    self.book=YamlMap('config/dustmail-addressbook.yaml')

  def start(self):
    msgs=self.displayList()

    command=None
    while command!='x':
      command=input('> ').strip()
      try:
        num=int(command)
        self.displayMessage(msgs[num-1][1])
      except:
        if command=='l':
          msgs=self.displayList()
        else:
          self.parseCommand(command)

  def displayList(self):
    #  msgs=os.listdir(maildir)
    msgs=[]
    for file in glob.glob(self.maildir + '/*.*'):
      stats = os.stat(file)
      lastmod_date = time.localtime(stats[8])
      date_file_tuple = lastmod_date, file
      msgs.append(date_file_tuple)

    if len(msgs)==0:
      print("No messages.")
    else:
      msgs.sort()
      msgs.reverse()

      for x in range(len(msgs)):
        date, fname=msgs[x]
        frm=fname.split('/')[-1].split('-')[0]
        modtime=time.strftime("%m/%d/%Y %I:%M%p",date)
        frmName=self.nameForPubkey(frm)
        if not frmName:
          frmName=frm
        print(str(x+1)+': '+frmName+' '+modtime)
    return msgs

  def nameForPubkey(self, pubkey):
    for name in self.book.keys():
      key=self.book[name]['pubkey']
      if key==pubkey:
        return name
    return None

  def displayMessage(self, fname):
    f=open(fname, 'r')
    msg=f.read()
    f.close()

    data=decode(msg)

    onion=OnionPacket()
    onion.decodeOnionPacket(self.endpoint.public.bytes, data)
    #  print(onion)
    print(onion.data.decode('ascii'))

  def parseCommand(self, command):
    self.commandDone.clear()
    if command=='x':
      self.commandDone.set()
      self.done.set()
      sys.exit(0)
    elif command=='a':
      self.addInvite()
    elif command=='i':
      self.makeInvite()
    elif command=='?':
      self.printHelp()
    waitForEvent(self.commandDone)

  def addInvite(self):
    pf=input("Load invite from Paste or File [P/f]? ")
    if pf=='f':
      filename=input("Load invite from filename: ").strip()
      f=open(filename, 'rb')
      data=f.read()
      f.close()
    else:
      data=decode(input("Past invite: "))

    passwd=input("Decrypt invite with password: ")
    packet=DustmailInvitePacket()
    packet.decodeDustmailInvitePacket(passwd, data)
    print("pubkey: "+encode(packet.pubkey))
    print("invite: "+encode(packet.invite))
    invite=InviteMessage()
    invite.decodeInviteMessage(packet.invite)
    self.keys.addInvite(invite)

    name=input("Name for this endpoint: ")
    try:
      entry=self.book[name]
    except:
      entry={}
    entry['pubkey']=encode(packet.pubkey)
    entry['tracker']=encodeAddress((invite.ip, invite.port))
    self.book[name]=entry

    self.commandDone.set()

  def makeInvite(self):
    self.tracker=TrackerClient(self.router)
    self.trackback=self.router.getService('trackback')
    self.trackback.setPutTrackerInviteCallback(self.gotInvite)
    self.tracker.getTrackerInvite()

  def gotInvite(self, invite):
    print('gotInvite')
    time.sleep(1)
    print()
    ps=input("Print or Save [P/s]?")
    passwd=input("Encrypt invite with password: ")
    packet=DustmailInvitePacket()
    packet.createDustmailInvitePacket(passwd, self.endpoint.public.bytes, invite, self.keys.entropy)
    if ps=='s':
      filename=input("Save invite to filename: ").strip()
      if filename!='':
        f=open(filename, 'wb')
        f.write(packet.packet)
        f.close()
    else:
      print()
      print(encode(packet.packet))
      print()

    self.commandDone.set()

  def printHelp(self):
    print('num: read message num')
    print('x: quit')
    print('l: list messages')
    print('a: add invite')
    print('i: make invite')

    self.commandDone.set()

if __name__=='__main__':
  passwd=input("Password: ")

  config=YamlMap('config/dustmail-config.yaml')

  try:
    inport=config['port']
  except:
    inport=7002
    config['port']=inport

  try:
    destAddress=config['tracker']
  except:
    destAddress='[2001:470:1f0e:63a::2]:7040'
    config['tracker']=destAddress

  dest, outport, v6=decodeAddress(destAddress)

  keys=KeyManager()
  keys.setInvitePassword(passwd)
  keys.loadKnownHosts('config/knownhosts.yaml')

  try:
    keys.loadKeypair('config/id.yaml')
  except:
    print('Generating server keypair...')
    keys.createKeypair()
    keys.saveKeypair('config/id.yaml')

  keys.loadIncomingInvites('config/incoming_invites.ip')
  keys.loadOutgoingInvites('config/outgoing_invites.ip')

  try:
    endpoint=keys.loadEndpoint(os.path.expanduser('~/.dust/endpoint.yaml'))
  except:
    print('Generating endpoint keypair...')
    keys.createKeypair()
    dustdir=os.path.expanduser("~/.dust")
    if not os.path.exists(dustdir):
      os.mkdir(dustdir)
    keys.saveKeypair(dustdir+'/endpoint.yaml')

  router=PacketRouter(v6, inport, keys, passwd)
  router.connect(dest, outport)
  router.start()

  reader=DustmailReader(router, endpoint)
  reader.start()

  waitForEvent(reader.done)
