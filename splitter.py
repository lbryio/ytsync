import os
import sys
from decimal import Decimal
from bitcoinrpc.authproxy import AuthServiceProxy

from lbryum.wallet import Wallet, WalletStorage
from lbryum.commands import known_commands, Commands
from lbryum.simple_config import SimpleConfig
from lbryum.blockchain import get_blockchain
from lbryum.network import Network


def get_lbrycrdd_connection_string(wallet_conf):
    settings = {"username": "rpcuser",
                "password": "rpcpassword",
                "rpc_port": 9245}
    if wallet_conf and os.path.exists(wallet_conf):
        with open(wallet_conf, "r") as conf:
            conf_lines = conf.readlines()
        for l in conf_lines:
            if l.startswith("rpcuser="):
                settings["username"] = l[8:].rstrip('\n')
            if l.startswith("rpcpassword="):
                settings["password"] = l[12:].rstrip('\n')
            if l.startswith("rpcport="):
                settings["rpc_port"] = int(l[8:].rstrip('\n'))

    rpc_user = settings["username"]
    rpc_pass = settings["password"]
    rpc_port = settings["rpc_port"]
    rpc_url = "127.0.0.1"
    return "http://%s:%s@%s:%i" % (rpc_user, rpc_pass, rpc_url, rpc_port)


class LBRYumWallet(object):
    def __init__(self, lbryum_path):
        self.config = SimpleConfig()
        self.config.set_key('chain', 'lbrycrd_main')
        self.storage = WalletStorage(lbryum_path)
        self.wallet = Wallet(self.storage)
        self.cmd_runner = Commands(self.config, self.wallet, None)
        if not self.wallet.has_seed():
            seed = self.wallet.make_seed()
            self.wallet.add_seed(seed, "derp")
            self.wallet.create_master_keys("derp")
            self.wallet.create_main_account()
            self.wallet.update_password("derp", "")
        self.network = Network(self.config)
        self.blockchain = get_blockchain(self.config, self.network)
        print self.config.get('chain'), self.blockchain
        self.wallet.storage.write()

    def command(self, command_name, *args, **kwargs):
        cmd_runner = Commands(self.config, self.wallet, None)
        cmd = known_commands[command_name]
        func = getattr(cmd_runner, cmd.name)
        return func(*args, **kwargs)

    def generate_address(self):
        address = self.wallet.create_new_address()
        self.wallet.storage.write()
        return address


class LBRYcrd(object):
    def __init__(self, lbrycrdd_path):
        self.lbrycrdd_conn_str = get_lbrycrdd_connection_string(lbrycrdd_path)

    def __call__(self, method, *args, **kwargs):
        return self.rpc(method)(*args, **kwargs)

    def rpc(self, method):
        return AuthServiceProxy(self.lbrycrdd_conn_str, service_name=method)


def get_wallet_path():
    cwd = os.getcwd()
    wallet_path = os.path.join(cwd, "wallet.json")
    if not os.path.exists(wallet_path):
        return wallet_path
    i = 1
    while True:
        wallet_path = os.path.join(cwd, "wallet_%i.json" % i)
        if not os.path.exists(wallet_path):
            return wallet_path
        i += 1


def coin_chooser(lbrycrdd, amount, fee=0.001):
    def iter_txis():
        unspent = lbrycrdd("listunspent")
        unspent = sorted(unspent, key=lambda x: x['amount'], reverse=True)
        spendable = Decimal(0.0)
        for txi in unspent:
            if spendable >= amount:
                break
            else:
                spendable += txi['amount']
                yield txi
        if spendable < amount:
            print spendable, amount
            raise Exception("Not enough funds")

    coins = list(iter(iter_txis()))
    total = sum(c['amount'] for c in coins)
    change = Decimal(total) - Decimal(amount) - Decimal(fee)

    if change < 0:
        raise Exception("Not enough funds")
    if change:
        change_address = lbrycrdd("getnewaddress")
    else:
        change_address = None

    print "Total: %f, amount: %f, change: %f" % (total, amount, change)

    return coins, change, change_address


def get_raw_tx(lbrycrdd, addresses, coins, amount, change, change_address):
    txi = [{'txid': c['txid'], 'vout': c['vout']} for c in coins]
    txo = {address: float(amount) for address in addresses}
    if change_address:
        txo[change_address] = float(change)
    return lbrycrdd("createrawtransaction", txi, txo)


def main(count, value=None, lbryum_path=None, lbrycrdd_path=None):
    count = int(count)
    lbryum_path = lbryum_path or get_wallet_path()
    if sys.platform == "darwin":
        default_lbrycrdd = os.path.join(os.path.expanduser("~"),
                                 "Library/Application Support/lbrycrd/lbrycrd.conf")
    else:
        default_lbrycrdd = os.path.join(os.path.expanduser("~"), ".lbrycrd/lbrycrd.conf")
    lbrycrdd_path = lbrycrdd_path or default_lbrycrdd
    l = LBRYcrd(lbrycrdd_path=lbrycrdd_path)
    s = LBRYumWallet(lbryum_path)
    value = value or 1.0
    value = Decimal(value)

    coins, change, change_address = coin_chooser(l, count * value)
    addresses = [s.generate_address() for i in range(count)]
    raw_tx = get_raw_tx(l, addresses, coins, value, change, change_address)
    signed = l("signrawtransaction", raw_tx)['hex']
    txid = l("sendrawtransaction", signed)
    print txid


if __name__ == "__main__":
    args = sys.argv[1:]
    main(*args)
