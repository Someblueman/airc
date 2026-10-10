package adapter

func socketEndpointsUnnamed(_ int, local, peer string) (bool, error) {
	return local == "" && peer == "", nil
}
